package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type configRequest struct {
	Action    string             `json:"action"`
	Target    string             `json:"target"`
	Changes   map[string]*string `json:"changes,omitempty"`
	Expected  string             `json:"expected,omitempty"`
	Confirmed bool               `json:"confirmed,omitempty"`
}
type configEntry struct {
	Name        string   `json:"name"`
	Target      string   `json:"target"`
	Type        string   `json:"type"`
	Default     string   `json:"default"`
	Description string   `json:"description"`
	Allowed     []string `json:"allowed"`
	Secret      bool     `json:"secret"`
	Fixed       bool     `json:"fixed"`
	Requirement string   `json:"requirement"`
	Effect      string   `json:"effect"`
	Value       string   `json:"value"`
	SavedValue  string   `json:"savedValue"`
	Source      string   `json:"source"`
	SaveSource  string   `json:"saveSource"`
	Explicit    bool     `json:"explicit"`
	Overridden  bool     `json:"overridden"`
}
type configResponse struct {
	Entries         []configEntry          `json:"entries,omitempty"`
	Expected        string                 `json:"expected,omitempty"`
	DatabaseChanged bool                   `json:"databaseChanged,omitempty"`
	DatabaseTarget  string                 `json:"databaseTarget,omitempty"`
	Saved           bool                   `json:"saved,omitempty"`
	Changes         []string               `json:"changes,omitempty"`
	Target          string                 `json:"target,omitempty"`
	Environment     map[string]string      `json:"environment,omitempty"`
	Error           string                 `json:"error,omitempty"`
	Applied         bool                   `json:"applied,omitempty"`
	Message         string                 `json:"message,omitempty"`
	Running         map[string]configEntry `json:"running,omitempty"`
}
type configRunner func(context.Context, configRequest) (configResponse, error)

func backendConfigRunner(ctx context.Context, cmd *cobra.Command, root string) (configRunner, func(), error) {
	process, cleanup, err := backendProcess(ctx, cmd, root, "aginex-tool", "config-protocol")
	if err != nil {
		return nil, nil, err
	}
	binary := process.Path
	return func(ctx context.Context, req configRequest) (configResponse, error) {
		data, err := json.Marshal(req)
		if err != nil {
			return configResponse{}, err
		}
		p := exec.CommandContext(ctx, binary, "config-protocol")
		p.Dir = root
		p.Stdin = bytes.NewReader(data)
		var out bytes.Buffer
		p.Stdout = &out
		var stderr bytes.Buffer
		p.Stderr = &stderr
		if err := p.Run(); err != nil {
			return configResponse{}, fmt.Errorf("configuration helper failed: %w", err)
		}
		var result configResponse
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			return result, errors.New("configuration helper returned an invalid response")
		}
		if result.Error != "" {
			return result, errors.New(result.Error)
		}
		return result, nil
	}, cleanup, nil
}
func configCommand() *cobra.Command {
	var directory string
	parent := &cobra.Command{Use: "config", Short: "Inspect and update server/admin startup configuration"}
	parent.PersistentFlags().StringVarP(&directory, "directory", "C", ".", "project directory")
	var target string
	var asJSON bool
	list := &cobra.Command{Use: "list", Short: "List setting names and current values", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return withConfigRunner(cmd, directory, func(run configRunner) error {
			result, err := run(cmd.Context(), configRequest{Action: "list", Target: target})
			if err != nil {
				return err
			}
			return printConfig(cmd, result, asJSON)
		})
	}}
	list.Flags().StringVar(&target, "target", "", "server or admin (default: both)")
	list.Flags().BoolVar(&asJSON, "json", false, "machine-readable output with secrets redacted")
	get := &cobra.Command{Use: "get <server|admin> <name>", Short: "Describe one setting and its effective source", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		return withConfigRunner(cmd, directory, func(run configRunner) error {
			r, e := run(cmd.Context(), configRequest{Action: "list", Target: args[0]})
			if e != nil {
				return e
			}
			for _, item := range r.Entries {
				if item.Name == args[1] {
					r.Entries = []configEntry{item}
					return printConfig(cmd, r, true)
				}
			}
			return fmt.Errorf("unknown setting: %s", args[1])
		})
	}}
	set := &cobra.Command{Use: "set <server|admin> <name=value|name>...", Short: "Validate, save to the real source and restart managed dev processes", Long: "Set a batch of startup settings. Omit =value to enter a value interactively (secrets are hidden). Database changes always require a successful connection test and interactive confirmation.", Args: cobra.MinimumNArgs(2)}
	unset := &cobra.Command{Use: "unset <server|admin> <name>...", Short: "Remove dotenv assignments and restore lower-priority values", Args: cobra.MinimumNArgs(2)}
	for _, command := range []*cobra.Command{set, unset} {
		command.RunE = func(cmd *cobra.Command, args []string) error {
			return withConfigRunner(cmd, directory, func(run configRunner) error { return changeConfig(cmd, run, args, cmd.Name() == "unset") })
		}
	}
	recover := &cobra.Command{Use: "recover", Short: "Recover an interrupted configuration file transaction", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return withConfigRunner(cmd, directory, func(run configRunner) error {
			_, err := run(cmd.Context(), configRequest{Action: "recover"})
			if err == nil {
				fmt.Fprintln(cmd.OutOrStdout(), "Configuration transaction recovered; run config set to apply any further changes.")
			}
			return err
		})
	}}
	parent.AddCommand(list, get, set, unset, recover)
	return parent
}
func withConfigRunner(cmd *cobra.Command, directory string, action func(configRunner) error) error {
	root, err := projectRoot(directory)
	if err != nil {
		return err
	}
	remote, online, err := connectDevConfig(root)
	if err != nil {
		return err
	}
	if online {
		return action(remote)
	}
	run, cleanup, err := backendConfigRunner(cmd.Context(), cmd, root)
	if err != nil {
		return err
	}
	defer cleanup()
	return action(run)
}
func changeConfig(cmd *cobra.Command, run configRunner, args []string, unset bool) error {
	listing, err := run(cmd.Context(), configRequest{Action: "list", Target: args[0]})
	if err != nil {
		return err
	}
	catalog := map[string]configEntry{}
	for _, e := range listing.Entries {
		catalog[e.Name] = e
	}
	request := configRequest{Action: "prepare", Target: args[0], Changes: map[string]*string{}}
	for _, arg := range args[1:] {
		name, value, hasValue := strings.Cut(arg, "=")
		item, ok := catalog[name]
		if !ok {
			return fmt.Errorf("unknown setting: %s", name)
		}
		if _, ok := request.Changes[name]; ok {
			return fmt.Errorf("duplicate setting: %s", name)
		}
		if unset {
			if hasValue {
				return errors.New("unset accepts names only")
			}
			request.Changes[name] = nil
			continue
		}
		if !hasValue {
			value, err = readConfigInput(cmd, name, item.Secret)
			if err != nil {
				return err
			}
		}
		request.Changes[name] = &value
	}
	preview, err := run(cmd.Context(), request)
	if err != nil {
		return err
	}
	if preview.DatabaseChanged {
		fmt.Fprintf(cmd.OutOrStdout(), "Connection test succeeded: %s\nNo files have been changed. Startup may initialize the target; existing data is not transferred.\n", preview.DatabaseTarget)
		input, ok := cmd.InOrStdin().(*os.File)
		if !ok || !term.IsTerminal(int(input.Fd())) {
			return errors.New("database changes require an interactive terminal; configuration unchanged")
		}
		fmt.Fprint(cmd.OutOrStdout(), "Apply this database connection and type? [y/N]: ")
		answer, err := bufio.NewReader(input).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if strings.ToLower(strings.TrimSpace(answer)) != "y" && strings.ToLower(strings.TrimSpace(answer)) != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "Cancelled; configuration unchanged.")
			return nil
		}
		request.Confirmed = true
	}
	request.Action = "commit"
	request.Expected = preview.Expected
	result, err := run(cmd.Context(), request)
	if err != nil {
		if result.Saved {
			fmt.Fprintln(cmd.ErrOrStderr(), "Configuration saved, but application restart failed. Correct the configuration and retry.")
		}
		return err
	}
	if result.Applied {
		fmt.Fprintln(cmd.OutOrStdout(), "Configuration saved and applied; managed development services are ready.")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Configuration saved. No managed development session is running; changes take effect on next startup.")
	}
	for name := range request.Changes {
		if name == "NEXT_PUBLIC_API_URL" {
			fmt.Fprintln(cmd.OutOrStdout(), "Production admin assets must be rebuilt for NEXT_PUBLIC_API_URL changes.")
		}
	}
	return nil
}
func readConfigInput(cmd *cobra.Command, name string, secret bool) (string, error) {
	input, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(input.Fd())) {
		return "", errors.New("interactive input requires a terminal")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: ", name)
	if secret {
		value, err := term.ReadPassword(int(input.Fd()))
		fmt.Fprintln(cmd.OutOrStdout())
		return string(value), err
	}
	value, err := bufio.NewReader(input).ReadString('\n')
	return strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r"), err
}
func printConfig(cmd *cobra.Command, r configResponse, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(r)
	}
	sort.Slice(r.Entries, func(i, j int) bool {
		return r.Entries[i].Target+r.Entries[i].Name < r.Entries[j].Target+r.Entries[j].Name
	})
	out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(out, "TARGET\tNAME\tVALUE")
	for _, e := range r.Entries {
		fmt.Fprintf(out, "%s\t%s\t%s\n", e.Target, e.Name, e.Value)
	}
	return out.Flush()
}
