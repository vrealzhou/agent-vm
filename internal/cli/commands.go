package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/vrealzhou/agent-vm/internal/container"
	"github.com/vrealzhou/agent-vm/internal/credential"
	"github.com/vrealzhou/agent-vm/internal/proxy"
	"github.com/vrealzhou/agent-vm/internal/secrets"
)

// Run builds the root command and executes it against os.Args.
func Run() error {
	root := &urfavecli.Command{
		Name:  "agent-vm",
		Usage: "Apple Container-based development environments",
		Commands: []*urfavecli.Command{
			newBuildCommand(),
			newStartCommand(),
			newStopCommand(),
			newRestartCommand(),
			newExecCommand(),
			newListCommand(),
			newDestroyCommand(),
			newWebCommand(),
			newSecretsCommand(),
			newProxyCommand(),      // hidden
			newCredentialCommand(), // hidden
			newKafkaProxyCommand(), // hidden
		},
	}
	return root.Run(context.Background(), os.Args)
}

// nameArg returns the first positional arg if present, otherwise the value of
// the named string flag.
func nameArg(cmd *urfavecli.Command) string {
	if cmd.Args().Len() > 0 {
		return cmd.Args().First()
	}
	return cmd.String("name")
}

func newBuildCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "build",
		Usage: "Build the container image",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return container.Build(cmd.String("go-version"))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:    "go-version",
				Aliases: []string{"g"},
				Usage:   "Go version to install (default: from Dockerfile)",
			},
		},
	}
}

func newStartCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "start",
		Usage:     "Start a development container and attach to it",
		ArgsUsage: "[name]",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			name := nameArg(cmd)
			if err := container.Start(name, cmd.String("cpus"), cmd.String("workspace"), !cmd.Bool("no-proxy"), cmd.StringSlice("publish"), cmd.String("profile")); err != nil {
				return err
			}
			if cmd.Bool("detach") {
				return nil
			}
			return container.Exec(name, cmd.String("workspace"))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   container.DefaultName,
				Usage:   "container name",
			},
			&urfavecli.StringFlag{
				Name:    "cpus",
				Aliases: []string{"c"},
				Value:   "6",
				Usage:   "number of CPUs",
			},
			&urfavecli.StringFlag{
				Name:    "workspace",
				Aliases: []string{"w"},
				Usage:   "host folder to map as $HOME/workspace",
			},
			&urfavecli.BoolFlag{
				Name:    "detach",
				Aliases: []string{"d"},
				Usage:   "start without attaching",
			},
			&urfavecli.BoolFlag{
				Name:  "no-proxy",
				Usage: "skip credential proxy",
			},
			&urfavecli.StringSliceFlag{
				Name:    "publish",
				Aliases: []string{"p"},
				Usage:   "publish port (host:container, repeatable)",
			},
			// Config profile resolution: selects which proxy.yaml to load.
			&urfavecli.StringFlag{
				Name:  "profile",
				Usage: "configuration profile to resolve (project > profile > global)",
			},
		},
	}
}

func newStopCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "stop",
		Usage:     "Stop a running container",
		ArgsUsage: "[name]",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return container.Stop(nameArg(cmd))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   container.DefaultName,
				Usage:   "container name",
			},
		},
	}
}

func newRestartCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "restart",
		Usage:     "Restart a container and attach to it",
		ArgsUsage: "[name]",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			name := nameArg(cmd)
			_ = container.Stop(name)
			if err := container.Start(name, cmd.String("cpus"), cmd.String("workspace"), !cmd.Bool("no-proxy"), cmd.StringSlice("publish"), cmd.String("profile")); err != nil {
				return err
			}
			return container.Exec(name, cmd.String("workspace"))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   container.DefaultName,
				Usage:   "container name",
			},
			&urfavecli.StringFlag{
				Name:    "cpus",
				Aliases: []string{"c"},
				Value:   "6",
				Usage:   "number of CPUs",
			},
			&urfavecli.StringFlag{
				Name:    "workspace",
				Aliases: []string{"w"},
				Usage:   "host folder to map as $HOME/workspace",
			},
			&urfavecli.BoolFlag{
				Name:  "no-proxy",
				Usage: "skip credential proxy",
			},
			&urfavecli.StringSliceFlag{
				Name:    "publish",
				Aliases: []string{"p"},
				Usage:   "publish port (host:container, repeatable)",
			},
			&urfavecli.StringFlag{
				Name:  "profile",
				Usage: "configuration profile to resolve (project > profile > global)",
			},
		},
	}
}

func newExecCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "exec",
		Usage:     "Attach to a running container",
		ArgsUsage: "[name]",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return container.Exec(nameArg(cmd), cmd.String("workspace"))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   container.DefaultName,
				Usage:   "container name",
			},
			&urfavecli.StringFlag{
				Name:    "workspace",
				Aliases: []string{"w"},
				Usage:   "host folder to map as $HOME/workspace",
			},
		},
	}
}

func newListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:    "list",
		Aliases: []string{"status", "ls"},
		Usage:   "List all containers",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return container.Status()
		},
	}
}

func newDestroyCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "destroy",
		Usage:     "Remove a container",
		ArgsUsage: "[name]",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return container.Destroy(nameArg(cmd))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   container.DefaultName,
				Usage:   "container name",
			},
		},
	}
}

func newWebCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "web",
		Usage: "Start the web portal for managing containers",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return container.Web(cmd.Int("port"))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.IntFlag{
				Name:    "port",
				Aliases: []string{"p"},
				Value:   8080,
				Usage:   "portal port",
			},
		},
	}
}

// --- secrets ---

func newSecretsCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "secrets",
		Usage: "Manage credential placeholders",
		Commands: []*urfavecli.Command{
			newSecretsAddCommand(),
			newSecretsListCommand(),
			newSecretsRemoveCommand(),
			newSecretsShowCommand(),
		},
	}
}

func newSecretsAddCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "add",
		Usage:     "Add or update a credential placeholder",
		ArgsUsage: "<name>",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("secrets add requires a <name> argument")
			}
			typeName := cmd.String("type")
			if typeName == "" {
				return fmt.Errorf("--type is required")
			}
			fields, err := parseFields(cmd.StringSlice("field"))
			if err != nil {
				return err
			}
			store, err := secrets.Load()
			if err != nil {
				return fmt.Errorf("load secrets: %w", err)
			}
			store.Add(name, secrets.Placeholder{Type: typeName, Fields: fields})
			if err := store.Save(); err != nil {
				return fmt.Errorf("save secrets: %w", err)
			}
			fmt.Printf("[agent-vm] saved placeholder %q (type: %s)\n", name, typeName)
			printFieldSummary(typeName, fields)
			return nil
		},
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:     "type",
				Aliases:  []string{"t"},
				Usage:    "credential type",
				Required: true,
			},
			&urfavecli.StringSliceFlag{
				Name:    "field",
				Aliases: []string{"f"},
				Usage:   "field as key=value (repeatable)",
			},
		},
	}
}

func newSecretsListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "list",
		Usage: "List all credential placeholders",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			store, err := secrets.Load()
			if err != nil {
				return fmt.Errorf("load secrets: %w", err)
			}
			names := store.List()
			sort.Strings(names)
			if len(names) == 0 {
				fmt.Println("No credential placeholders.")
				return nil
			}
			fmt.Printf("%-20s  %-14s  %s\n", "NAME", "TYPE", "FIELDS")
			for _, name := range names {
				p, _ := store.Get(name)
				fmt.Printf("%-20s  %-14s  %s\n", name, p.Type, describeFields(p))
			}
			return nil
		},
	}
}

func newSecretsRemoveCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "remove",
		Usage:     "Remove a credential placeholder",
		ArgsUsage: "<name>",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("secrets remove requires a <name> argument")
			}
			store, err := secrets.Load()
			if err != nil {
				return fmt.Errorf("load secrets: %w", err)
			}
			if !store.Remove(name) {
				return fmt.Errorf("no placeholder named %q", name)
			}
			if err := store.Save(); err != nil {
				return fmt.Errorf("save secrets: %w", err)
			}
			fmt.Printf("[agent-vm] removed placeholder %q\n", name)
			return nil
		},
	}
}

func newSecretsShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "show",
		Usage:     "Show details of a credential placeholder",
		ArgsUsage: "<name>",
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			name := cmd.Args().First()
			if name == "" {
				return fmt.Errorf("secrets show requires a <name> argument")
			}
			store, err := secrets.Load()
			if err != nil {
				return fmt.Errorf("load secrets: %w", err)
			}
			p, ok := store.Get(name)
			if !ok {
				return fmt.Errorf("no placeholder named %q", name)
			}
			var display map[string]string
			if cmd.Bool("reveal") {
				display = p.Fields
			} else {
				display = secrets.MaskedFields(p)
			}
			fmt.Printf("Name:  %s\n", name)
			fmt.Printf("Type:  %s\n", p.Type)
			fmt.Println("Fields:")
			keys := make([]string, 0, len(display))
			for k := range display {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				secret := secrets.IsSecretField(p.Type, k)
				marker := ""
				if secret {
					marker = " (secret)"
				}
				fmt.Printf("  %s%s = %s\n", k, marker, display[k])
			}
			return nil
		},
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{
				Name:  "reveal",
				Usage: "show actual secret values instead of masking",
			},
		},
	}
}

// --- helpers ---

// parseFields parses repeated "key=value" strings into a map.
func parseFields(raw []string) (map[string]string, error) {
	fields := make(map[string]string)
	for _, f := range raw {
		idx := strings.Index(f, "=")
		if idx < 0 {
			return nil, fmt.Errorf("invalid field %q: expected key=value", f)
		}
		fields[f[:idx]] = f[idx+1:]
	}
	return fields, nil
}

// printFieldSummary reports which stored fields are plaintext vs secret.
func printFieldSummary(typeName string, fields map[string]string) {
	var plaintext, secret []string
	for k := range fields {
		if secrets.IsSecretField(typeName, k) {
			secret = append(secret, k)
		} else {
			plaintext = append(plaintext, k)
		}
	}
	sort.Strings(plaintext)
	sort.Strings(secret)
	if len(plaintext) > 0 {
		fmt.Printf("  plaintext: %s\n", strings.Join(plaintext, ", "))
	}
	if len(secret) > 0 {
		fmt.Printf("  secret:    %s\n", strings.Join(secret, ", "))
	}
}

// describeFields renders a compact summary of a placeholder's fields, marking
// secret fields with an asterisk.
func describeFields(p secrets.Placeholder) string {
	keys := make([]string, 0, len(p.Fields))
	for k := range p.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		if secrets.IsSecretField(p.Type, k) {
			parts = append(parts, k+"*")
		} else {
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, ", ")
}

// --- hidden internal commands ---

func newProxyCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:   "_proxy",
		Usage:  "Internal: run credential proxy daemon",
		Hidden: true,
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return proxy.RunProxyServer(cmd.Int("port"))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.IntFlag{
				Name:    "port",
				Aliases: []string{"p"},
				Usage:   "proxy listen port",
			},
		},
	}
}

func newCredentialCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:   "_credential",
		Usage:  "Internal: run credential forwarding daemon",
		Hidden: true,
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return credential.RunCredentialServer(cmd.Int("port"))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.IntFlag{
				Name:    "port",
				Aliases: []string{"p"},
				Usage:   "credential server listen port",
			},
		},
	}
}

func newKafkaProxyCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:   "_kafka-proxy",
		Usage:  "Internal: run Kafka SASL credential TCP proxy",
		Hidden: true,
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			return proxy.RunKafkaProxyServer(cmd.Int("port"))
		},
		Flags: []urfavecli.Flag{
			&urfavecli.IntFlag{
				Name:    "port",
				Aliases: []string{"p"},
				Usage:   "kafka proxy listen port",
			},
		},
	}
}
