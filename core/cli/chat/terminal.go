package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

func runTerminalChat(ctx context.Context, session *chatSession, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	if err := writeChat(out, "LocalAI chat (%s)\n", session.CurrentModel()); err != nil {
		return err
	}
	if err := writeChat(out, "Type /exit to quit, /clear to reset the conversation, /models to list models.\n"); err != nil {
		return err
	}

	for {
		if err := writeChat(out, "\n> "); err != nil {
			return err
		}
		if !scanner.Scan() {
			break
		}

		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/bye", "/exit", "/quit":
			return writeChat(out, "bye\n")
		case "/clear":
			session.Clear()
			if err := writeChat(out, "conversation cleared\n"); err != nil {
				return err
			}
			continue
		case "/models":
			if err := printChatModels(out, session.Models(), session.CurrentModel()); err != nil {
				return err
			}
			continue
		}

		if nextModel, ok := strings.CutPrefix(prompt, "/model "); ok {
			nextModel = strings.TrimSpace(nextModel)
			if nextModel == "" {
				if err := writeChat(out, "usage: /model <name>\n"); err != nil {
					return err
				}
				continue
			}
			if err := session.SwitchModel(nextModel); err != nil {
				if writeErr := writeChat(out, "%s\n", err); writeErr != nil {
					return writeErr
				}
				continue
			}
			if err := writeChat(out, "switched to %s; conversation cleared\n", session.CurrentModel()); err != nil {
				return err
			}
			continue
		}

		if err := writeChat(out, "assistant: "); err != nil {
			return err
		}
		if err := session.Send(ctx, prompt, out); err != nil {
			return err
		}
		if err := writeChat(out, "\n"); err != nil {
			return err
		}
	}

	return scanner.Err()
}

func printChatModels(out io.Writer, models []string, current string) error {
	if len(models) == 0 {
		return writeChat(out, "no models installed\n")
	}
	return writeChat(out, "%s", formatChatModelList(models, current))
}

func writeChat(out io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(out, format, args...)
	return err
}
