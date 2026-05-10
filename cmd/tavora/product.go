package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var productCmd = &cobra.Command{
	Use:   "product",
	Short: "Product operations",
}

var productShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current product",
	RunE: func(cmd *cobra.Command, args []string) error {
		product, err := client.GetProduct(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(product)
		}

		detail(fmt.Sprintf("Product: %s", product.Name),
			field("ID", product.ID),
			field("Slug", product.Slug),
			field("Description", product.Description),
			field("Created", product.CreatedAt.Format("2006-01-02 15:04:05")),
		)
		return nil
	},
}

var productSeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Ensure the product has the platform-invariant default agent (idempotent)",
	Long: `Products created via signup get a default agent + version + eval suite
auto-provisioned. Products created via SDK or admin paths sometimes don't
(historical gap). This command runs the same SeedStarter that signup runs,
idempotently — if any agent already exists, it reports already_seeded and
mutates nothing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := client.SeedProduct(cmd.Context())
		if err != nil {
			return err
		}
		if isJSON() {
			return printJSON(res)
		}
		if res.AlreadySeeded {
			fmt.Printf("Product already has agent %q (%s); no changes made.\n", res.AgentName, res.AgentID)
		} else {
			fmt.Printf("Seeded product with default agent %q (%s).\n", res.AgentName, res.AgentID)
		}
		return nil
	},
}

func init() {
	productCmd.AddCommand(productShowCmd)
	productCmd.AddCommand(productSeedCmd)
}
