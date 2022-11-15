package jprovd

import (
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/spf13/cobra"
)

func StartServer() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "start jackal storage provider",
		Long:  `Start jackal storage provider`,
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			StartFileServer(cmd)
			return nil
		},
	}

	flags.AddTxFlagsToCmd(cmd)
	cmd.Flags().String("storagedir", "~/.canine/networkfiles", "location to host your files")
	cmd.Flags().String("port", "3333", "port to host the server on")
	cmd.Flags().Bool("debug", false, "allow printing info messages from the storage provider daemon")
	cmd.Flags().Uint16("interval", 30, "the interval in seconds for which to check proofs")

	return cmd
}
