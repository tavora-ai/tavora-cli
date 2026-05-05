package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var (
	searchIndexID string
	searchTopK         int32
	searchMinScore     float64
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Semantic search across documents",
	Example: `  tavora search "how does authentication work"
  tavora search "deployment guide" --store abc123 --top-k 5
  tavora search "error handling" --min-score 0.5 --output json`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")

		results, err := client.Search(cmd.Context(), tavora.SearchInput{
			Query:   query,
			IndexID: searchIndexID,
			TopK:         searchTopK,
			MinScore:     searchMinScore,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(results)
		}

		if len(results) == 0 {
			fmt.Println("No results found.")
			return nil
		}

		fmt.Printf("Found %d results:\n\n", len(results))
		for i, r := range results {
			fmt.Printf("--- Result %d (score: %.3f) ---\n", i+1, r.Score)
			fmt.Printf("Document: %s (chunk %d)\n", r.Filename, r.ChunkIndex)
			fmt.Printf("%s\n\n", r.Content)
		}
		return nil
	},
}

func init() {
	searchCmd.Flags().StringVar(&searchIndexID, "store", "", "Filter by store ID")
	searchCmd.Flags().Int32Var(&searchTopK, "top-k", 10, "Number of results")
	searchCmd.Flags().Float64Var(&searchMinScore, "min-score", 0.3, "Minimum similarity score")
}
