package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strings"

	"github.com/spf13/cobra"
	"github.com/tarcisiomiranda/devrig/internal/docker"
	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/out"
)

var (
	appVersion = "0.1.1"

	flagPort     int
	flagUser     string
	flagPassword string
	flagDB       string
	flagImage    string
	flagTimeout  time.Duration
	flagTail     int
	flagEngine   string
)

// SetVersion sets the binary version string.
func SetVersion(v string) {
	if v != "" {
		appVersion = v
	}
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "devrig",
	Short: "Manage throwaway databases for integration tests",
	Long: "devrig creates named database containers via the Docker Engine API for\n" +
		"local/integration tests (Linux + macOS).\n\n" +
		"Engines: postgres (ready). mysql and mariadb are planned — see TODO.md.",
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
	upCmd.Flags().StringVar(&flagEngine, "engine", string(engine.Postgres),
		"database engine: "+strings.Join(engine.Known(), ", ")+" (implemented: "+strings.Join(engine.Implemented(), ", ")+")")
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
	Use:   "up <name>",
	Short: "Create or reuse a named database instance (JSON)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		spec, err := engine.Lookup(flagEngine)
		if err != nil {
			out.Fatal(err)
		}
		withClient(func(ctx context.Context, c *docker.Client) error {
			st, err := c.Up(ctx, docker.UpOptions{
				Name:     args[0],
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
