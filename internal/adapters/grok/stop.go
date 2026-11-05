package grok

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/state"
)

// StopInput is the part of a Grok Stop event this adapter reads.
type StopInput struct {
	Session       string
	Project       string
	Reason        string
	Subagent      string
	LastAssistant string
}

// Stop blocks an idle end_turn until there is something to show, or returns
// the receipt reason. announce reports that the reason carried the identity
// and the caller should clear that flag only after the reason is written out.
func Stop(ctx context.Context, root string, in StopInput) (reason string, announce bool, err error) {
	if in.Subagent != "" || in.Reason != "end_turn" {
		return "", false, nil
	}
	agent, err := Resolve(root, in.Session, in.Project)
	if err != nil || agent == "" {
		return "", false, err
	}
	client, err := state.Open(root, agent)
	if err != nil {
		return "", false, err
	}
	client.PollInterval = PollInterval
	for {
		shown, err := loadPresentation(root, agent)
		if err != nil {
			return "", false, err
		}
		if shown != nil && shown.Token != "" {
			if shown.Session != in.Session {
				if ownerAlive(*shown) {
					shown.Claimant = in.Session
					return "", false, savePresentation(root, agent, *shown)
				}
				shown.Session = in.Session
			}
			accepted := finalLine(in.LastAssistant) == shown.Token && !truncated(in.LastAssistant)
			if accepted {
				if err := client.AcknowledgeDeliveryReceipt(shown.Receipt); err != nil {
					return "", false, err
				}
				if err := deletePresentation(root, agent); err != nil {
					return "", false, err
				}
				in.LastAssistant = ""
				continue
			}
			text := deliveryReason(shown.Token, mustPresentationPath(root, agent))
			due, err := announcementDue(root, in.Session)
			if err != nil {
				return "", false, err
			}
			if due {
				text += "\n\n" + Announcement(in.Session)
			}
			return clip(text), due, nil
		}
		if due, err := announcementDue(root, in.Session); err != nil {
			return "", false, err
		} else if due {
			return Announcement(in.Session), true, nil
		}
		if ResidentReleased(root, agent, in.Session) {
			return "", false, nil
		}
		if !ResidentAlive(root, agent, in.Session) {
			return "", false, ErrResidentDown
		}
		timer := time.NewTimer(PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", false, nil
		case <-timer.C:
		}
	}
}

// Resolve prefers the session binding, then the project's .conductor-agent file.
func Resolve(root, session, project string) (string, error) {
	if session != "" {
		agent, err := binding.Identity(root, AdapterName, session)
		if err != nil || agent != "" {
			return agent, err
		}
	}
	if project == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(project, ".conductor-agent"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(string(data), "\xef\xbb\xbf")), nil
}

// Announcement is the identity text a session start, or the next end of turn, shows.
func Announcement(session string) string {
	return "Conductor adapter grok is holding this session. If this is not your identity, run: conductor adapter grok bind <your-agent-name>. Session " + session + "."
}

func announcementDue(root, session string) (bool, error) {
	again, err := flagSet(root, "reannounce", session)
	if err != nil || again {
		return again, err
	}
	announced, err := flagSet(root, "announced", session)
	return !announced, err
}

// ClearAnnouncement removes the flags once the identity text has been written out.
func ClearAnnouncement(root, session string) error {
	if err := clearFlag(root, "reannounce", session); err != nil {
		return err
	}
	return setFlag(root, "announced", session)
}

func deliveryReason(token, path string) string {
	return token + "\n" + path + "\nRead the file, act on it, and end your reply with that token as the final nonblank line."
}

func clip(text string) string {
	if len(text) <= 9000 {
		return text
	}
	return text[:9000]
}

func finalLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func truncated(text string) bool {
	return strings.Contains(text, "… [+")
}
