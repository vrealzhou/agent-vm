package vmctl

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func NewRootCommand() (*cobra.Command, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}

	rootCmd := &cobra.Command{
		Use:           "agent-vm",
		Short:         "Agent VM — a reproducible Void Linux dev VM on Apple Silicon",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			port, _ := cmd.Flags().GetString("port")
			return LaunchWebServer(port)
		},
	}
	rootCmd.Flags().StringP("port", "p", "", "web UI port (default: 8080)")
	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == rootCmd {
			fmt.Fprint(cmd.OutOrStdout(), Usage(cfg))
		} else {
			defaultHelp(cmd, args)
		}
	})

	rootCmd.AddCommand(
		newStartCommand(cfg),
		newStopCommand(cfg),
		newDestroyCommand(cfg),
		newStatusCommand(cfg),
		newGUICommand(cfg),
		newBootstrapCommand(cfg),
		newSSHCommand(cfg),
		newIPCommand(cfg),
		newSyncCommand(cfg),
		newTunnelCommand(cfg),
		newBuildImageCommand(cfg),
	)

	return rootCmd, nil
}

func newStartCommand(cfg Config) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Create missing assets and start the VM or container",
		Args:  leafArgs,
		RunE: leafRunE(func(cmd *cobra.Command, args []string) error {
			return NewBackend(cfg).Start(cfg)
		}),
	}
}

func newStopCommand(cfg Config) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the VM or container",
		Args:  leafArgs,
		RunE: leafRunE(func(cmd *cobra.Command, args []string) error {
			return NewBackend(cfg).Stop(cfg)
		}),
	}
}

func newDestroyCommand(cfg Config) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Stop and remove the VM or container and its state",
		Args:  leafArgs,
		RunE: leafRunE(func(cmd *cobra.Command, args []string) error {
			return NewBackend(cfg).Destroy(cfg)
		}),
	}
}

func newStatusCommand(cfg Config) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show VM or container state",
		Args:  leafArgs,
		RunE: leafRunE(func(cmd *cobra.Command, args []string) error {
			return Status(cfg)
		}),
	}
}

func newGUICommand(cfg Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gui",
		Short: "Open the Web VM control panel",
		Args:  leafArgs,
		RunE: leafRunE(func(cmd *cobra.Command, args []string) error {
			return LaunchWebServer("")
		}),
	}
	cmd.Flags().String("port", "", "Server port (default: 8080 or VM_MANAGER_PORT env)")
	return cmd
}

func newBootstrapCommand(cfg Config) *cobra.Command {
	var hookFiles []string
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Configure fish + Homebrew + Docker + desktop tools inside the guest over SSH",
		Args:  leafArgs,
		RunE: leafRunE(func(cmd *cobra.Command, args []string) error {
			for _, path := range hookFiles {
				content, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("failed to read hook file %q: %w", path, err)
				}
				if cfg.BootstrapExtraCommands != "" {
					cfg.BootstrapExtraCommands += "\n"
				}
				cfg.BootstrapExtraCommands += string(content)
			}
			return BootstrapSetup(cfg)
		}),
	}
	cmd.Flags().StringArrayVar(&hookFiles, "hook", nil, "path to a shell script to execute as a post-bootstrap hook (repeatable)")
	return cmd
}

func newSSHCommand(cfg Config) *cobra.Command {
	return &cobra.Command{
		Use:                "ssh [ssh args...]",
		Short:              "SSH into the guest using the configured static IP (vm@" + cfg.StaticIP + ")",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && args[0] == "help" {
				return cmd.Help()
			}
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return cmd.Help()
				}
			}
			return SSH(cfg, args)
		},
	}
}

func newIPCommand(cfg Config) *cobra.Command {
	var setIP string
	cmd := &cobra.Command{
		Use:   "ip",
		Short: "Print or set the guest IP address",
		Args:  leafArgs,
		RunE: leafRunE(func(cmd *cobra.Command, args []string) error {
			if setIP != "" {
				cfg.StaticIP = setIP
				if err := SaveConfig(cfg); err != nil {
					return fmt.Errorf("failed to save config: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "IP updated to %s\n", setIP)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), cfg.StaticIP)
			return nil
		}),
	}
	cmd.Flags().StringVar(&setIP, "set", "", "set guest IP address in vmctl.yaml")
	return cmd
}

func newBuildImageCommand(cfg Config) *cobra.Command {
	var profileName string
	var profileFile string
	var buildAll bool
	cmd := &cobra.Command{
		Use:   "build-image",
		Short: "Build a container image from an environment profile",
		Args:  leafArgs,
		RunE: leafRunE(func(cmd *cobra.Command, args []string) error {
			if buildAll {
				profiles, err := ListProfiles(cfg.ConfigDir)
				if err != nil {
					return fmt.Errorf("failed to list profiles: %w", err)
				}
				for _, p := range profiles {
					if err := buildProfileImage(cfg, p); err != nil {
						return err
					}
				}
				return nil
			}
			if profileFile != "" {
				p, err := LoadProfile(profileFile)
				if err != nil {
					return err
				}
				return buildProfileImage(cfg, p)
			}
			if profileName != "" {
				p, err := ResolveProfile(cfg.ConfigDir, profileName)
				if err != nil {
					return err
				}
				return buildProfileImage(cfg, p)
			}
			if cfg.Environment != "" {
				p, err := ResolveProfile(cfg.ConfigDir, cfg.Environment)
				if err != nil {
					return err
				}
				return buildProfileImage(cfg, p)
			}
			return fmt.Errorf("specify --profile <name>, --file <path>, or --all")
		}),
	}
	cmd.Flags().StringVar(&profileName, "profile", "", "environment profile name")
	cmd.Flags().StringVar(&profileFile, "file", "", "path to a profile YAML file")
	cmd.Flags().BoolVar(&buildAll, "all", false, "build all profiles")
	return cmd
}

func buildProfileImage(cfg Config, profile EnvironmentProfile) error {
	imageName := ProfileImageName(profile)
	logf("building image %s from profile %s", imageName, profile.Name)
	addProgress("generating Dockerfile for %s...", profile.Name)
	if err := WriteBuildContext(cfg, profile); err != nil {
		return fmt.Errorf("failed to write build context: %w", err)
	}

	cli := "podman"
	if _, err := exec.LookPath("podman"); err != nil {
		if _, err := exec.LookPath("docker"); err != nil {
			return fmt.Errorf("neither podman nor docker found")
		}
		cli = "docker"
	}

	addProgress("building image %s (this may take several minutes)...", imageName)
	cmd := exec.Command(cli, "build", "-t", imageName, cfg.StateDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("image build failed: %w", err)
	}
	addProgress("image %s built successfully", imageName)
	return nil
}

func leafRunE(fn func(cmd *cobra.Command, args []string) error) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 && args[0] == "help" {
			return cmd.Help()
		}
		return fn(cmd, args)
	}
}

func leafArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 1 && args[0] == "help" {
		return nil
	}
	return cobra.NoArgs(cmd, args)
}
