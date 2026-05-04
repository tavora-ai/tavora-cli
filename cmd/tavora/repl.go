package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
)

// repl runs an interactive read-eval-print loop with readline support.
// It persists history to ~/.tavora_history and supports up/down arrows.
// Stops when the user types "exit", "quit", or hits Ctrl-D.
func repl(prompt string, onInput func(input string) error) error {
	historyFile := ""
	if home, err := os.UserHomeDir(); err == nil {
		historyFile = filepath.Join(home, ".tavora_history")
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		HistoryFile:     historyFile,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		// Fall back to simple scanner if readline fails
		return replFallback(prompt, onInput)
	}
	defer rl.Close()

	fmt.Println("Type a message and press Enter. Use 'exit' or Ctrl-D to quit.")
	fmt.Println()

	for {
		line, err := rl.Readline()
		if err != nil {
			// EOF (Ctrl-D) or interrupt
			fmt.Println()
			return nil
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}

		if err := onInput(line); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", wrapError(err))
		}
		fmt.Println()
	}
}

// replFallback is a basic REPL without readline (used if readline init fails).
func replFallback(prompt string, onInput func(input string) error) error {
	scanner := readline.NewCancelableStdin(os.Stdin)
	defer scanner.Close()

	fmt.Println("Type a message and press Enter. Use 'exit' or Ctrl-D to quit.")
	fmt.Println()

	buf := make([]byte, 4096)
	for {
		fmt.Print(prompt)
		n, err := scanner.Read(buf)
		if err != nil {
			fmt.Println()
			return nil
		}

		line := strings.TrimSpace(string(buf[:n]))
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}

		if err := onInput(line); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", wrapError(err))
		}
		fmt.Println()
	}
}
