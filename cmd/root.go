package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/image"
	"github.com/tarcisiomiranda/devrig/internal/kube"
	"github.com/tarcisiomiranda/devrig/internal/out"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

var (
	appVersion = "0.3.0"

	flagPort          int
	flagContainerPort int

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
	Short: "Run disposable dev dependencies in Docker or Kubernetes",
	Long: "devrig starts named instances of the dependencies a dev loop needs,\n" +
		"either as Docker containers or as Kubernetes pods (Linux + macOS).\n\n" +
		"Backends: docker (Docker Engine API) and kube (kubectl: a pod plus a\n" +
		"port-forward, so the URL stays on 127.0.0.1). Without --backend, devrig\n" +
		"uses Docker when it answers and falls back to kubectl.\n\n" +
		"Resources: postgres (ready). mysql and mariadb are planned — see TODO.md.\n\n" +
		"Images: build, push and login drive the podman or docker CLI (auto prefers\n" +
		"podman) and read registry credentials from .env through confctl.",
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

	rootCmd.PersistentFlags().StringVar(&flagBackend, "backend", "",
		"where instances run: docker, kube or auto (default: $DEVRIG_BACKEND, else auto)")
	rootCmd.PersistentFlags().StringVarP(&flagNamespace, "namespace", "n", "",
		"kube backend: namespace (default: $DEVRIG_NAMESPACE, else the context's)")
	rootCmd.PersistentFlags().StringVar(&flagContext, "context", "",
		"kube backend: kubeconfig context (default: $DEVRIG_CONTEXT, else the current one)")
	rootCmd.PersistentFlags().StringVar(&flagKubeconfig, "kubeconfig", "",
		"kube backend: kubeconfig file (default: $DEVRIG_KUBECONFIG, else kubectl's own resolution)")

	listCmd.Flags().BoolVarP(&flagAllNamespaces, "all-namespaces", "A", false,
		"kube backend: list managed pods in every namespace")

	upCmd.Flags().StringVar(&flagCPU, "cpu", "",
		"kube backend: pod cpu request and limit (default: "+kube.DefaultCPURequest+" request, "+kube.DefaultCPULimit+" limit)")
	upCmd.Flags().StringVar(&flagMemory, "memory", "",
		"kube backend: pod memory request and limit (default: "+kube.DefaultMemoryRequest+" request, "+kube.DefaultMemoryLimit+" limit)")

	upCmd.Flags().IntVar(&flagPort, "port", 0, "host port (0 = ephemeral)")
	upCmd.Flags().IntVar(&flagContainerPort, "container-port", 0,
		"port the resource listens on inside the container (default: the engine's; needs an --image that actually listens there)")
	upCmd.Flags().StringVar(&flagName, "name", "",
		"instance name (default: the resource name, so `up postgres` is named postgres)")
	// Registered though removed in v0.2.0, so the error can name the new syntax.
	upCmd.Flags().StringVar(&flagEngineGone, "engine", "", "")
	_ = upCmd.Flags().MarkHidden("engine")
	upCmd.Flags().StringVar(&flagUser, "user", "", "database user (default: test)")
	upCmd.Flags().StringVar(&flagPassword, "password", "", "database password (default: test)")
	upCmd.Flags().StringVar(&flagDB, "db", "", "database name (default: <name>_test)")
	upCmd.Flags().StringVar(&flagImage, "image", "", "container image (default: the engine's)")
	upCmd.Flags().DurationVar(&flagTimeout, "timeout", 0,
		"ready wait timeout (default: 30s on docker, "+kube.DefaultTimeout.String()+" on kube, where the node may still be pulling the image)")

	logsCmd.Flags().IntVar(&flagTail, "tail", 100, "number of log lines")
}

var upCmd = &cobra.Command{
	Use:   "up <resource>",
	Short: "Create or reuse a named instance of a resource (JSON)",
	Long: "Start a resource and wait until it answers queries.\n\n" +
		"  devrig up postgres                        # instance named \"postgres\"\n" +
		"  devrig up postgres --name valid-vfa       # several instances of one resource\n" +
		"  devrig up postgres --backend kube -n dev  # a pod plus a local port-forward\n\n" +
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
		withBackend(func(ctx context.Context, c rig.Backend) error {
			st, err := c.Up(ctx, rig.UpOptions{
				Name:          name,
				Engine:        spec,
				User:          flagUser,
				Password:      flagPassword,
				Database:      flagDB,
				Image:         flagImage,
				Port:          flagPort,
				ContainerPort: flagContainerPort,
				Timeout:       flagTimeout,
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
		withBackend(func(ctx context.Context, c rig.Backend) error {
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
		withBackend(func(ctx context.Context, c rig.Backend) error {
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
		withBackend(func(ctx context.Context, c rig.Backend) error {
			items, err := c.List(ctx)
			if err != nil {
				return err
			}
			if items == nil {
				items = []rig.Status{}
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
		withBackend(func(ctx context.Context, c rig.Backend) error {
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
		withBackend(func(ctx context.Context, c rig.Backend) error {
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
			"name":     "devrig",
			"version":  appVersion,
			"backends": []string{rig.KindDocker.String(), rig.KindKube.String()},
			"tools":    image.Known(),
			"engines":  engine.Implemented(),
			"planned":  []string{string(engine.MySQL), string(engine.MariaDB)},
		})
	},
}
