// Copyright 2019-present Open Networking Foundation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package command

import (
	"fmt"
	"github.com/atomix/go-client/pkg/client/value"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

func newValueCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "value <name> [...]",
		Short:              "Manage the state of a distributed value",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// If only the name was specified, open an interactive shell
			name := args[0]
			if name == "-h" || name == "--help" {
				return cmd.Help()
			}
			if len(args) == 1 {
				ctx := getContext()
				if ctx == nil {
					ctx = newContext("atomix", "value", name)
					setContext(ctx)
				} else {
					ctx = ctx.withCommand("value", name)
				}
				return ctx.run()
			}

			// Get the command for the specified operation
			var subCmd *cobra.Command
			op := args[1]
			switch op {
			case "set":
				subCmd = newValueSetCommand(name)
			case "get":
				subCmd = newValueGetCommand(name)
			case "watch":
				subCmd = newValueWatchCommand(name)
			case "help", "-h", "--help":
				if len(args) == 2 {
					helpCmd := &cobra.Command{
						Use:   fmt.Sprintf("value %s [...]", name),
						Short: "Manage the state of a distributed value",
					}
					helpCmd.AddCommand(newValueSetCommand(name))
					helpCmd.AddCommand(newValueGetCommand(name))
					helpCmd.AddCommand(newValueWatchCommand(name))
					return helpCmd.Help()
				} else {
					var helpCmd *cobra.Command
					switch args[2] {
					case "set":
						helpCmd = newValueSetCommand(name)
					case "get":
						helpCmd = newValueGetCommand(name)
					case "watch":
						helpCmd = newValueWatchCommand(name)
					default:
						return fmt.Errorf("unknown command %s", args[2])
					}
					return helpCmd.Help()
				}
			default:
				return fmt.Errorf("unknown command %s", op)
			}
			addClientFlags(subCmd)

			// Set the arguments after the name and execute the command
			subCmd.SetArgs(args[2:])
			return subCmd.Execute()
		},
	}
	return cmd
}

func getValue(cmd *cobra.Command, name string) (value.Value, error) {
	database, err := getDatabase(cmd)
	if err != nil {
		return nil, err
	}
	ctx, cancel := getTimeoutContext(cmd)
	defer cancel()
	return database.GetValue(ctx, name)
}

func newValueSetCommand(name string) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "set <value>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			val, err := getValue(cmd, name)
			if err != nil {
				return err
			}

			version, _ := cmd.Flags().GetUint64("version")
			ctx, cancel := getTimeoutContext(cmd)
			defer cancel()
			var newVersion uint64
			if version > 0 {
				newVersion, err = val.Set(ctx, []byte(args[0]), value.IfVersion(version))
			} else {
				newVersion, err = val.Set(ctx, []byte(args[0]))
			}

			if err != nil {
				return err
			}
			cmd.Println(newVersion)
			return nil
		},
	}
	cmd.Flags().Uint64("version", 0, "the value version to update (for optimistic locking)")
	return cmd
}

func newValueGetCommand(name string) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "get",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			val, err := getValue(cmd, name)
			if err != nil {
				return err
			}

			version, _ := cmd.Flags().GetUint64("version")
			ctx, cancel := getTimeoutContext(cmd)
			defer cancel()
			value, version, err := val.Get(ctx)
			if err != nil {
				return err
			}

			bytes, err := yaml.Marshal(map[string]interface{}{"value": value, "version": version})
			if err != nil {
				return err
			}
			cmd.Println(string(bytes))
			return nil
		},
	}
	return cmd
}

func newValueWatchCommand(name string) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "watch",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			val, err := getValue(cmd, name)
			if err != nil {
				return err
			}

			watchCh := make(chan *value.Event)
			ctx, cancel := getCancelContext(cmd)
			defer cancel()
			if err := val.Watch(ctx, watchCh); err != nil {
				return err
			}

			for event := range watchCh {
				bytes, err := yaml.Marshal(event)
				if err != nil {
					cmd.Println(err)
				} else {
					cmd.Println(string(bytes))
				}
			}
			return nil
		},
	}
	return cmd
}
