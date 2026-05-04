package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	tavora "github.com/tavora-ai/tavora-sdk-go"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Chat completions and conversations",
}

// --- Stateless completions ---

var (
	chatModel        string
	chatSystem       string
	chatUseRAG       bool
	chatStoreID string
)

var chatCompleteCmd = &cobra.Command{
	Use:   "complete [message]",
	Short: "Send a single chat completion (stateless)",
	Example: `  tavora chat complete "What is Tavora?"
  tavora chat complete "Summarize the docs" --rag --store abc123
  tavora chat complete "Explain this code" --system "You are a code reviewer"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		message := strings.Join(args, " ")

		var messages []tavora.ChatMessage
		if chatSystem != "" {
			messages = append(messages, tavora.ChatMessage{Role: "system", Content: chatSystem})
		}
		messages = append(messages, tavora.ChatMessage{Role: "user", Content: message})

		input := tavora.ChatCompletionInput{
			Model:    chatModel,
			Messages: messages,
			UseRAG:   chatUseRAG,
		}
		if chatStoreID != "" {
			input.StoreID = chatStoreID
		}

		result, err := client.ChatCompletion(cmd.Context(), input)
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(result)
		}

		if len(result.Choices) > 0 {
			fmt.Println(result.Choices[0].Message.Content)
		}

		status("\n[%s | %d prompt + %d completion = %d tokens]",
			result.Model, result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)
		return nil
	},
}

// --- Conversations ---

var conversationsCmd = &cobra.Command{
	Use:   "conversations",
	Short: "Manage server-side conversations",
}

var (
	convListLimit int
)

var conversationsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List conversations",
	RunE: func(cmd *cobra.Command, args []string) error {
		convs, err := client.ListConversations(cmd.Context(), convListLimit, 0)
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(convs)
		}

		if len(convs) == 0 {
			fmt.Println("No conversations found.")
			return nil
		}

		t := newTable("ID", "TITLE", "MODEL", "UPDATED")
		for _, c := range convs {
			title := c.Title
			if title == "" {
				title = "(untitled)"
			}
			t.row(c.ID, title, c.Model, c.UpdatedAt.Format("2006-01-02 15:04"))
		}
		return t.flush()
	},
}

var (
	convCreateTitle  string
	convCreateSystem string
	convCreateModel  string
)

var conversationsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a conversation",
	RunE: func(cmd *cobra.Command, args []string) error {
		conv, err := client.CreateConversation(cmd.Context(), tavora.CreateConversationInput{
			Title:        convCreateTitle,
			SystemPrompt: convCreateSystem,
			Model:        convCreateModel,
		})
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(conv)
		}

		fmt.Printf("Created conversation: %s\n", conv.ID)
		if conv.Title != "" {
			fmt.Printf("  Title: %s\n", conv.Title)
		}
		fmt.Printf("  Model: %s\n", conv.Model)
		return nil
	},
}

var conversationsGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Get a conversation with messages",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := client.GetConversation(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(detail)
		}

		title := detail.Conversation.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("Conversation: %s\n", title)
		fmt.Printf("  ID:    %s\n", detail.Conversation.ID)
		fmt.Printf("  Model: %s\n", detail.Conversation.Model)

		if detail.TokenUsage != nil {
			fmt.Printf("  Tokens: %d total\n", detail.TokenUsage.TotalTokens)
		}

		if len(detail.Messages) > 0 {
			fmt.Printf("\n--- Messages (%d) ---\n\n", len(detail.Messages))
			for _, msg := range detail.Messages {
				fmt.Printf("[%s] %s\n\n", msg.Role, msg.Content)
			}
		}
		return nil
	},
}

var (
	sendUseRAG       bool
	sendStoreID string
)

var conversationsSendCmd = &cobra.Command{
	Use:   "send [conversation-id] [message]",
	Short: "Send a message in a conversation",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		convID := args[0]
		message := strings.Join(args[1:], " ")

		input := tavora.SendMessageInput{
			Content: message,
			UseRAG:  sendUseRAG,
		}
		if sendStoreID != "" {
			input.StoreID = sendStoreID
		}

		result, err := client.SendMessage(cmd.Context(), convID, input)
		if err != nil {
			return err
		}

		if isJSON() {
			return printJSON(result)
		}

		fmt.Println(result.Message.Content)
		status("\n[%d prompt + %d completion = %d tokens]",
			result.TokenUsage.PromptTokens, result.TokenUsage.CompletionTokens, result.TokenUsage.TotalTokens)
		return nil
	},
}

var conversationsDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a conversation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.DeleteConversation(cmd.Context(), args[0]); err != nil {
			return err
		}

		if isJSON() {
			return printJSON(map[string]string{"status": "deleted"})
		}

		fmt.Println("Conversation deleted.")
		return nil
	},
}

var (
	interactiveSystem       string
	interactiveModel        string
	interactiveUseRAG       bool
	interactiveStoreID string
)

var chatInteractiveCmd = &cobra.Command{
	Use:   "interactive",
	Short: "Interactive chat REPL (creates a server-side conversation)",
	Example: `  tavora chat interactive
  tavora chat interactive --rag --system "You are a helpful assistant"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		conv, err := client.CreateConversation(cmd.Context(), tavora.CreateConversationInput{
			Title:        "CLI Chat",
			SystemPrompt: interactiveSystem,
			Model:        interactiveModel,
		})
		if err != nil {
			return err
		}

		fmt.Printf("Conversation: %s (model: %s)\n", conv.ID, conv.Model)

		return repl("you> ", func(input string) error {
			msgInput := tavora.SendMessageInput{
				Content: input,
				UseRAG:  interactiveUseRAG,
			}
			if interactiveStoreID != "" {
				msgInput.StoreID = interactiveStoreID
			}

			result, err := client.SendMessage(cmd.Context(), conv.ID, msgInput)
			if err != nil {
				return err
			}

			fmt.Printf("\n%s\n", result.Message.Content)
			status("[%d prompt + %d completion = %d tokens]",
				result.TokenUsage.PromptTokens, result.TokenUsage.CompletionTokens, result.TokenUsage.TotalTokens)
			return nil
		})
	},
}

func init() {
	chatCompleteCmd.Flags().StringVar(&chatModel, "model", "", "AI model (default: gemini-2.5-flash)")
	chatCompleteCmd.Flags().StringVar(&chatSystem, "system", "", "System prompt")
	chatCompleteCmd.Flags().BoolVar(&chatUseRAG, "rag", false, "Enable RAG from space documents")
	chatCompleteCmd.Flags().StringVar(&chatStoreID, "store", "", "Limit RAG to store ID")

	chatInteractiveCmd.Flags().StringVar(&interactiveSystem, "system", "", "System prompt")
	chatInteractiveCmd.Flags().StringVar(&interactiveModel, "model", "", "AI model")
	chatInteractiveCmd.Flags().BoolVar(&interactiveUseRAG, "rag", false, "Enable RAG from space documents")
	chatInteractiveCmd.Flags().StringVar(&interactiveStoreID, "store", "", "Limit RAG to store ID")

	conversationsListCmd.Flags().IntVar(&convListLimit, "limit", 20, "Max conversations to return")

	conversationsCreateCmd.Flags().StringVar(&convCreateTitle, "title", "", "Conversation title")
	conversationsCreateCmd.Flags().StringVar(&convCreateSystem, "system", "", "System prompt")
	conversationsCreateCmd.Flags().StringVar(&convCreateModel, "model", "", "AI model")

	conversationsSendCmd.Flags().BoolVar(&sendUseRAG, "rag", false, "Enable RAG")
	conversationsSendCmd.Flags().StringVar(&sendStoreID, "store", "", "Limit RAG to store ID")

	conversationsCmd.AddCommand(conversationsListCmd)
	conversationsCmd.AddCommand(conversationsCreateCmd)
	conversationsCmd.AddCommand(conversationsGetCmd)
	conversationsCmd.AddCommand(conversationsSendCmd)
	conversationsCmd.AddCommand(conversationsDeleteCmd)

	chatCmd.AddCommand(chatCompleteCmd)
	chatCmd.AddCommand(chatInteractiveCmd)
	chatCmd.AddCommand(conversationsCmd)
}
