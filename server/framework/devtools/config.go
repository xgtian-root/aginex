package devtools

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/xgtian-root/aginex/server/internal/configcli"
)

func newConfigProtocolCommand() *cobra.Command {
	return &cobra.Command{Use: "config-protocol", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var request configcli.Request
		decoder := json.NewDecoder(io.LimitReader(cmd.InOrStdin(), 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return fmt.Errorf("invalid configuration request")
		}
		root, err := os.Getwd()
		if err != nil {
			return err
		}
		var response configcli.Response
		if request.Action == "recover" {
			err = configcli.Recover(root)
		} else {
			response, err = configcli.Execute(cmd.Context(), root, request)
		}
		if err != nil {
			response.Error = err.Error()
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(response)
	}}
}
