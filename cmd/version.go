package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/jfox85/devx/version"
	"github.com/spf13/cobra"
)

var (
	versionOutput string
	detailedFlag  bool
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  `Display version information for devx including build details.`,
	Run:   runVersion,
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().StringVarP(&versionOutput, "output", "o", "", "Output format: json")
	versionCmd.Flags().BoolVar(&detailedFlag, "detailed", false, "Show detailed version information")
}

func runVersion(cmd *cobra.Command, args []string) {
	info := version.Get()

	switch versionOutput {
	case "json":
		output, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			fmt.Printf("Error formatting JSON: %v\n", err)
			return
		}
		fmt.Println(string(output))
	default:
		if detailedFlag {
			fmt.Println(info.Detailed())
		} else {
			fmt.Println(info.String())
		}
	}
}
