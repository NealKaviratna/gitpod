// Copyright (c) 2020 Gitpod GmbH. All rights reserved.
// Licensed under the GNU Affero General Public License (AGPL).
// See License-AGPL.txt in the project root for license information.

package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/golang/protobuf/ptypes"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	csapi "github.com/gitpod-io/gitpod/content-service/api"
	"github.com/gitpod-io/gitpod/loadgen/pkg/loadgen"
	"github.com/gitpod-io/gitpod/loadgen/pkg/observer"
	"github.com/gitpod-io/gitpod/ws-manager/api"
)

var runOpts struct {
	TLSPath string
}

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "runs the load generator",
	Run: func(cmd *cobra.Command, args []string) {
		const workspaceCount = 500

		var load loadgen.LoadGenerator
		load = loadgen.NewFixedLoadGenerator(500*time.Millisecond, 300*time.Millisecond)
		load = loadgen.NewWorkspaceCountLimitingGenerator(load, workspaceCount)

		template := &api.StartWorkspaceRequest{
			Id: "will-be-overriden",
			Metadata: &api.WorkspaceMetadata{
				MetaId:    "will-be-overriden",
				Owner:     "00000000-0000-0000-0000-000000000000",
				StartedAt: ptypes.TimestampNow(),
			},
			ServicePrefix: "will-be-overriden",
			Spec: &api.StartWorkspaceSpec{
				IdeImage:         "eu.gcr.io/gitpod-dev/ide/theia:master.3206",
				Admission:        api.AdmissionLevel_ADMIT_OWNER_ONLY,
				CheckoutLocation: "gitpod",
				Git: &api.GitSpec{
					Email:    "test@gitpod.io",
					Username: "foobar",
				},
				FeatureFlags: []api.WorkspaceFeatureFlag{},
				Initializer: &csapi.WorkspaceInitializer{
					Spec: &csapi.WorkspaceInitializer_Git{
						Git: &csapi.GitInitializer{
							CheckoutLocation: "",
							CloneTaget:       "main",
							RemoteUri:        "https://github.com/gitpod-io/gitpod.git",
							TargetMode:       csapi.CloneTargetMode_REMOTE_BRANCH,
							Config: &csapi.GitConfig{
								Authentication: csapi.GitAuthMethod_NO_AUTH,
							},
						},
					},
				},
				Timeout:           "5m",
				WorkspaceImage:    "eu.gcr.io/gitpod-dev/workspace-images:3fcaad7ba5a5a4695782cb4c366b82f927f1e6c1cf0c88fd4f14d985f7eb21f6",
				WorkspaceLocation: "gitpod",
				Envvars: []*api.EnvironmentVariable{
					{
						Name:  "THEIA_SUPERVISOR_TOKENS",
						Value: `[{"token":"foobar","host":"gitpod-staging.com","scope":["function:getWorkspace","function:getLoggedInUser","function:getPortAuthenticationToken","function:getWorkspaceOwner","function:getWorkspaceUsers","function:isWorkspaceOwner","function:controlAdmission","function:setWorkspaceTimeout","function:getWorkspaceTimeout","function:sendHeartBeat","function:getOpenPorts","function:openPort","function:closePort","function:getLayout","function:generateNewGitpodToken","function:takeSnapshot","function:storeLayout","function:stopWorkspace","resource:workspace::fa498dcc-0a84-448f-9666-79f297ad821a::get/update","resource:workspaceInstance::e0a17083-6a78-441a-9b97-ef90d6aff463::get/update/delete","resource:snapshot::*::create/get","resource:gitpodToken::*::create","resource:userStorage::*::create/get/update"],"expiryDate":"2020-12-01T07:55:12.501Z","reuse":2}]`,
					},
				},
			},
			Type: api.WorkspaceType_REGULAR,
		}

		var opts []grpc.DialOption
		if runOpts.TLSPath != "" {
			ca, err := ioutil.ReadFile(filepath.Join(runOpts.TLSPath, "ca.crt"))
			if err != nil {
				log.Fatal(err)
			}
			capool := x509.NewCertPool()
			capool.AppendCertsFromPEM(ca)
			cert, err := tls.LoadX509KeyPair(filepath.Join(runOpts.TLSPath, "tls.crt"), filepath.Join(runOpts.TLSPath, "tls.key"))
			if err != nil {
				log.Fatal(err)
			}
			creds := credentials.NewTLS(&tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      capool,
				ServerName:   "ws-manager",
			})
			opts = append(opts, grpc.WithTransportCredentials(creds))
		} else {
			opts = append(opts, grpc.WithInsecure())
		}

		conn, err := grpc.Dial("localhost:8080", opts...)
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		client := api.NewWorkspaceManagerClient(conn)
		executor := &loadgen.WsmanExecutor{C: client}

		session := &loadgen.Session{
			Executor: executor,
			Load:     load,
			Specs:    &loadgen.FixedWorkspaceGenerator{Template: template},
			Worker:   5,
			Observer: []chan<- *loadgen.SessionEvent{
				observer.NewLogObserver(true),
				observer.NewProgressBarObserver(workspaceCount),
				observer.NewStatsObserver(func(s *observer.Stats) {
					fc, err := json.Marshal(s)
					if err != nil {
						return
					}
					os.WriteFile("stats.json", fc, 0644)
				}),
			},
			PostLoadWait: func() {
				<-make(chan struct{})
				log.Info("load generation complete - press Ctrl+C to finish of")

			},
		}

		go func() {
			sigc := make(chan os.Signal)
			signal.Notify(sigc, syscall.SIGINT)
			<-sigc
			os.Exit(0)
		}()

		err = session.Run()
		if err != nil {
			log.WithError(err).Fatal()
		}

	},
}

func init() {
	rootCmd.AddCommand(runCmd)

	runCmd.Flags().StringVar(&runOpts.TLSPath, "tls", "", "path to ws-manager's TLS certificates")
}
