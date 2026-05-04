package main

import (
	"fmt"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var storesCmd = &cobra.Command{
	Use:   "stores",
	Short: "Manage document stores",
}

var storesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stores",
	RunE: func(cmd *cobra.Command, args []string) error {
		stores, err := client.ListStores(cmd.Context())
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(stores)
		}

		if len(stores) == 0 {
			fmt.Println("No stores found.")
			return nil
		}

		t := newTable("ID", "NAME", "DESCRIPTION", "CREATED")
		for _, s := range stores {
			t.row(s.ID, s.Name, s.Description, s.CreatedAt.Format("2006-01-02"))
		}
		return t.flush()
	},
}

var (
	storeCreateName string
	storeCreateDesc string
)

var storesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a store",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := client.CreateStore(cmd.Context(), tavora.CreateStoreInput{
			Name:        storeCreateName,
			Description: storeCreateDesc,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(store)
		}

		fmt.Printf("Created store: %s (%s)\n", store.Name, store.ID)
		return nil
	},
}

var storesGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get a store by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := client.GetStore(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(store)
		}

		detail(fmt.Sprintf("Store: %s", store.Name),
			field("ID", store.ID),
			field("Description", store.Description),
			field("Created", store.CreatedAt.Format("2006-01-02 15:04:05")),
		)
		return nil
	},
}

var (
	storeUpdateName string
	storeUpdateDesc string
)

var storesUpdateCmd = &cobra.Command{
	Use:   "update [id]",
	Short: "Update a store",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := client.UpdateStore(cmd.Context(), args[0], tavora.UpdateStoreInput{
			Name:        storeUpdateName,
			Description: storeUpdateDesc,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(store)
		}

		fmt.Printf("Updated store: %s (%s)\n", store.Name, store.ID)
		return nil
	},
}

var storesDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a store by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeleteStore(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("Store deleted.")
		return nil
	},
}

func init() {
	storesCreateCmd.Flags().StringVar(&storeCreateName, "name", "", "Store name (required)")
	storesCreateCmd.Flags().StringVar(&storeCreateDesc, "description", "", "Store description")
	storesCreateCmd.MarkFlagRequired("name")

	storesUpdateCmd.Flags().StringVar(&storeUpdateName, "name", "", "Store name")
	storesUpdateCmd.Flags().StringVar(&storeUpdateDesc, "description", "", "Store description")

	storesCmd.AddCommand(storesListCmd)
	storesCmd.AddCommand(storesCreateCmd)
	storesCmd.AddCommand(storesGetCmd)
	storesCmd.AddCommand(storesUpdateCmd)
	storesCmd.AddCommand(storesDeleteCmd)
}
