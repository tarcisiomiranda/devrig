package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarcisiomiranda/devrig/internal/docker"
	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/out"
)

var (
	appVersion = "0.1.1"

	flagPort       int
	flagUser       string
	flagPassword   string
	flagDB         string
	flagImage      string
	flagTimeout    time.Duration
	flagTail       int
	flagName       string
	flagEngineGone string
)

// SetVersion sets the binary version string.
func SetVersion(v string) {
	if v != "" {
		appVersion = v
	}
}

// Execute runs the root command.
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "devrig: %v\nTry 'devrig --help'.\n", err)
		return err
	}
	return nil
}

var rootCmd = &cobra.Command{
	Use:   "devrig",
	Short: "Run disposable dev dependencies in Docker",
	Long: "devrig starts named containers for the dependencies a dev loop needs,\n" +
		"through the Docker Engine API (Linux + macOS).\n\n" +
		"Resources: postgres (ready). mysql and mariadb are planned — see TODO.md.",
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(urlCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(versionCmd)

	upCmd.Flags().IntVar(&flagPort, "port", 0, "host port (0 = ephemeral)")
	upCmd.Flags().StringVar(&flagName, "name", "",
		"instance name (default: the resource name, so `up postgres` is named postgres)")
	// Registered though removed in v0.2.0, so the error can name the new syntax.
	upCmd.Flags().StringVar(&flagEngineGone, "engine", "", "")
	_ = upCmd.Flags().MarkHidden("engine")
	upCmd.Flags().StringVar(&flagUser, "user", "", "database user (default: test)")
	upCmd.Flags().StringVar(&flagPassword, "password", "", "database password (default: test)")
	upCmd.Flags().StringVar(&flagDB, "db", "", "database name (default: <name>_test)")
	upCmd.Flags().StringVar(&flagImage, "image", "", "container image (default: the engine's)")
	upCmd.Flags().DurationVar(&flagTimeout, "timeout", 30*time.Second, "ready wait timeout")

	logsCmd.Flags().IntVar(&flagTail, "tail", 100, "number of log lines")
}

func withClient(fn func(ctx context.Context, c *docker.Client) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c, err := docker.New(ctx)
	if err != nil {
		out.Fatal(err)
	}
	defer c.Close()

	if err := fn(ctx, c); err != nil {
		out.Fatal(err)
	}
}

var upCmd = &cobra.Command{
	Use:   "up <resource>",
	Short: "Create or reuse a named instance of a resource (JSON)",
	Long: "Start a resource and wait until it answers queries.\n\n" +
		"  devrig up postgres                  # instance named \"postgres\"\n" +
		"  devrig up postgres --name valid-vfa # several instances of one resource\n\n" +
		"Resources: " + strings.Join(engine.Known(), ", ") +
		" (implemented: " + strings.Join(engine.Implemented(), ", ") + ")",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		resource := args[0]
		if flagEngineGone != "" {
			out.Fatalf("--engine was removed in v0.2.0: the resource comes first now\n"+
				"  devrig up %s --name %s", flagEngineGone, resource)
		}
		spec, err := engine.Lookup(resource)
		if err != nil {
			out.Fatal(migrationHint(resource, err))
		}
		name := flagName
		if name == "" {
			name = resource
		}
		withClient(func(ctx context.Context, c *docker.Client) error {
			st, err := c.Up(ctx, docker.UpOptions{
				Name:     name,
				Engine:   spec,
				User:     flagUser,
				Password: flagPassword,
				Database: flagDB,
				Image:    flagImage,
				Port:     flagPort,
				Timeout:  flagTimeout,
			})
			if err != nil {
				return err
			}
			out.JSON(st)
			return nil
		})
	},
}

func migrationHint(resource string, err error) error {
	var unknown *engine.ErrUnknown
	if !errors.As(err, &unknown) {
		return err // declared-but-unimplemented resource: the message already fits
	}
	return fmt.Errorf("%w\n\n"+
		"devrig up now takes the resource first. If %[2]q is an instance name:\n"+
		"  devrig up postgres --name %[2]s", err, resource)
}

var downCmd = &cobra.Command{
	Use:   "down <name>",
	Short: "Stop and remove a named instance (JSON, idempotent)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		withClient(func(ctx context.Context, c *docker.Client) error {
			res, err := c.Down(ctx, args[0])
			if err != nil {
				return err
			}
			out.JSON(res)
			return nil
		})
	},
}

var statusCmd = &cobra.Command{
	Use:   "status <name>",
	Short: "Show instance status (JSON)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		withClient(func(ctx context.Context, c *docker.Client) error {
			st, err := c.Status(ctx, args[0])
			if err != nil {
				return err
			}
			out.JSON(st)
			return nil
		})
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed instances (JSON)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		withClient(func(ctx context.Context, c *docker.Client) error {
			items, err := c.List(ctx)
			if err != nil {
				return err
			}
			if items == nil {
				items = []docker.Status{}
			}
			out.JSON(map[string]any{"instances": items})
			return nil
		})
	},
}

var urlCmd = &cobra.Command{
	Use:   "url <name>",
	Short: "Print connection URL only (for shell: export TEST_DATABASE_URL=$(devrig url name))",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		withClient(func(ctx context.Context, c *docker.Client) error {
			u, err := c.URL(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Println(u)
			return nil
		})
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Container logs (JSON)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		withClient(func(ctx context.Context, c *docker.Client) error {
			res, err := c.Logs(ctx, args[0], flagTail)
			if err != nil {
				return err
			}
			out.JSON(res)
			return nil
		})
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version (JSON)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		out.JSON(map[string]any{
			"name":    "devrig",
			"version": appVersion,
			"engines": engine.Implemented(),
			"planned": []string{string(engine.MySQL), string(engine.MariaDB)},
		})
	},
}
