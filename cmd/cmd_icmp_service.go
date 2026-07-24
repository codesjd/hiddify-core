package cmd

import (
	"fmt"

	"github.com/hiddify/hiddify-core/v2/hcore/icmpservice"

	"github.com/spf13/cobra"
)

var commandIcmpService = &cobra.Command{
	Use:       "icmp run/stop/exit",
	Short:     "Elevated ICMP helper run/stop/exit",
	ValidArgs: []string{"run", "stop", "exit"},
	Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	Run: func(cmd *cobra.Command, args []string) {
		arg := args[0]
		switch arg {
		case "run":
			code, out := icmpservice.StartIcmpService()
			fmt.Printf("exitCode:%d msg=%s", code, out)
		case "stop", "exit":
			icmpservice.ExitIcmpHelper()
		}
	},
}
