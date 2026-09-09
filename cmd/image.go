package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tarcisiomiranda/devrig/internal/envfile"
	"github.com/tarcisiomiranda/devrig/internal/image"
	"github.com/tarcisiomiranda/devrig/internal/out"
)

// defaultEnvFile is read when it exists and --env-file is not given.
const defaultEnvFile = ".env"

var (
	flagTool        string
	flagEnvFile     string
	flagNoLogin     bool
	flagUserKey     string
	flagPasswordKey string
	flagRegistryKey string

	flagBuildFile     string
	flagBuildTags     []string
	flagBuildPlatform string
	flagBuildArgs     []string
	flagBuildTarget   string
	flagBuildNoCache  bool
	flagBuildPush     bool
)

func init() {
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(pushCmd)
	rootCmd.AddCommand(loginCmd)

	for _, c := range []*cobra.Command{buildCmd, pushCmd, loginCmd} {
		c.Flags().StringVar(&flagTool, "tool", "",
			"CLI that builds and pushes: podman, docker or auto (default: $DEVRIG_TOOL, else auto — podman first, then docker)")
		c.Flags().StringVar(&flagEnvFile, "env-file", "",
			"file with registry credentials, read through confctl (default: "+defaultEnvFile+" when it exists)")
		c.Flags().StringVar(&flagUserKey, "user-key", "",
			"key holding the registry user (default: "+strings.Join(image.UserKeys, ", ")+")")
		c.Flags().StringVar(&flagPasswordKey, "password-key", "",
			"key holding the registry token or password (default: "+strings.Join(image.PasswordKeys, ", ")+")")
	}
	loginCmd.Flags().StringVar(&flagRegistryKey, "registry-key", "",
		"key holding the registry when none is given (default: "+strings.Join(image.RegistryKeys, ", ")+")")

	buildCmd.Flags().StringArrayVarP(&flagBuildTags, "tag", "t", nil, "image reference to tag (repeatable, at least one)")
	buildCmd.Flags().StringVarP(&flagBuildFile, "file", "f", "", "Containerfile/Dockerfile (default: the tool's own lookup in the context)")
	buildCmd.Flags().StringVar(&flagBuildPlatform, "platform", "", "target platform, e.g. linux/amd64")
	buildCmd.Flags().StringArrayVar(&flagBuildArgs, "build-arg", nil, "build argument KEY=VALUE (repeatable; never put a secret here)")
	buildCmd.Flags().StringVar(&flagBuildTarget, "target", "", "build stage to stop at")
	buildCmd.Flags().BoolVar(&flagBuildNoCache, "no-cache", false, "do not use the layer cache")
	buildCmd.Flags().BoolVar(&flagBuildPush, "push", false, "push every tag after the build (logs in from the env file)")
	buildCmd.Flags().BoolVar(&flagNoLogin, "no-login", false, "with --push: skip login, use the tool's existing session")

	pushCmd.Flags().BoolVar(&flagNoLogin, "no-login", false, "skip login and use the tool's existing session")
}

var buildCmd = &cobra.Command{
	Use:   "build [context]",
	Short: "Build an image with podman or docker (JSON)",
	Long: "Build an image with whichever tool the machine has. Build output goes to\n" +
		"stderr; stdout carries one JSON object.\n\n" +
		"  devrig build -t ghcr.io/org/app:dev .\n" +
		"  devrig build -t ghcr.io/org/app:dev --platform linux/amd64 --push\n\n" +
		"Tools: " + strings.Join(image.Known(), ", ") + " (auto prefers podman)",
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		buildContext := "."
		if len(args) == 1 {
			buildContext = args[0]
		}
		withTool(func(ctx context.Context, c *image.Client) error {
			res, err := c.Build(ctx, image.BuildOptions{
				Context:   buildContext,
				File:      flagBuildFile,
				Tags:      flagBuildTags,
				Platform:  flagBuildPlatform,
				BuildArgs: flagBuildArgs,
				Target:    flagBuildTarget,
				NoCache:   flagBuildNoCache,
			}, os.Stderr)
			if err != nil {
				return err
			}
			if flagBuildPush {
				sources, err := credentialSources(ctx, flagEnvFile, flagNoLogin)
				if err != nil {
					return err
				}
				res.Push, err = c.Push(ctx, image.PushOptions{
					Refs:    flagBuildTags,
					Login:   !flagNoLogin,
					Sources: sources,
					Keys:    keyOverrides(),
				}, os.Stderr)
				if err != nil {
					return err
				}
			}
			out.JSON(res)
			return nil
		})
	},
}

var pushCmd = &cobra.Command{
	Use:   "push <image> [<image>...]",
	Short: "Push images, logging in from .env when credentials are there (JSON)",
	Long: "Push one or more image references. For each registry devrig looks for\n" +
		"credentials in the env file (through confctl) and then in the environment,\n" +
		"logs in with the password on stdin, and pushes.\n\n" +
		"  devrig push ghcr.io/org/app:dev\n" +
		"  devrig push ghcr.io/org/app:dev --env-file deploy/.env\n" +
		"  devrig push ghcr.io/org/app:dev --no-login   # reuse the tool's session",
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		withTool(func(ctx context.Context, c *image.Client) error {
			sources, err := credentialSources(ctx, flagEnvFile, flagNoLogin)
			if err != nil {
				return err
			}
			res, err := c.Push(ctx, image.PushOptions{
				Refs:    args,
				Login:   !flagNoLogin,
				Sources: sources,
				Keys:    keyOverrides(),
			}, os.Stderr)
			if err != nil {
				return err
			}
			out.JSON(res)
			return nil
		})
	},
}

var loginCmd = &cobra.Command{
	Use:   "login [registry]",
	Short: "Log in to a registry with credentials from .env (JSON, never prints the token)",
	Long: "Read the user and the token from the env file through confctl (then from the\n" +
		"environment) and run `<tool> login --password-stdin`.\n\n" +
		"  devrig login ghcr.io\n" +
		"  devrig login                       # registry from REGISTRY in the env file\n" +
		"  devrig login ghcr.io --user-key GH_USER --password-key GH_PASS",
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		registry := ""
		if len(args) == 1 {
			registry = args[0]
		}
		withTool(func(ctx context.Context, c *image.Client) error {
			sources, err := credentialSources(ctx, flagEnvFile, false)
			if err != nil {
				return err
			}
			cred, err := image.Resolve(registry, keyOverrides(), sources)
			if err != nil {
				return err
			}
			res, err := c.Login(ctx, cred)
			if err != nil {
				return err
			}
			out.JSON(res)
			return nil
		})
	},
}

func keyOverrides() image.KeyOverrides {
	return image.KeyOverrides{Registry: flagRegistryKey, User: flagUserKey, Password: flagPasswordKey}
}

// resolveEnvFile returns the file to read: the flag as given (so a missing
// explicit file is reported), else the default when it exists, else nothing.
func resolveEnvFile(flag string, exists func(string) bool) string {
	if flag != "" {
		return flag
	}
	if exists(defaultEnvFile) {
		return defaultEnvFile
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// credentialSources reads the env file through confctl (when there is one to
// read) and appends the process environment. With noLogin nothing is read.
func credentialSources(ctx context.Context, envFile string, noLogin bool) ([]image.Source, error) {
	if noLogin {
		return nil, nil
	}
	var sources []image.Source
	if path := resolveEnvFile(envFile, fileExists); path != "" {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("env file: %w", err)
		}
		r, err := envfile.NewRunner(os.Getenv("DEVRIG_CONFCTL"))
		if err != nil {
			return nil, err
		}
		values, err := envfile.Read(ctx, r, path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, image.Source{Name: path, Values: values})
	}
	return append(sources, image.SourceFromEnviron(os.Environ())), nil
}

func toolOptions() (image.Options, error) {
	tool, err := image.ParseTool(flagTool, os.Getenv("DEVRIG_TOOL"))
	if err != nil {
		return image.Options{}, err
	}
	return image.Options{
		Tool:   tool,
		Podman: os.Getenv("DEVRIG_PODMAN"),
		Docker: os.Getenv("DEVRIG_DOCKER"),
	}, nil
}

func withTool(fn func(ctx context.Context, c *image.Client) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opt, err := toolOptions()
	if err != nil {
		out.Fatal(err)
	}
	c, err := image.New(ctx, opt)
	if err != nil {
		out.Fatal(err)
	}
	if err := fn(ctx, c); err != nil {
		out.Fatal(err)
	}
}
