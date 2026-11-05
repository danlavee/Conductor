// Package agy implements Conductor's wake adapter for Google Antigravity (AGY).
package agy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/adapters/binding"
	"github.com/danlavee/Conductor/internal/platform"
	"github.com/danlavee/Conductor/internal/state"
)

const (
	// AdapterName is the directory and CLI token for this host adapter.
	AdapterName = "agy"

	// SessionEnvironment is the conversation identifier Antigravity exports to
	// hook and model-started processes.
	SessionEnvironment = "ANTIGRAVITY_CONVERSATION_ID"

	// WaitTimeoutEnvironment bounds how long Stop blocks when set (e.g. in tests).
	WaitTimeoutEnvironment = "CONDUCTOR_AGY_WAIT_TIMEOUT"

	// HookCommandPlaceholder is rewritten at install time to the installed binary path.
	HookCommandPlaceholder = "${CONDUCTOR_BIN}"

	// IdentityFile is the project-level default agent identity file.
	IdentityFile = ".conductor-agent"

	receiptPrefix = "CONDUCTOR_RECEIPT "
)

// Resolution describes the resolved session and project bindings for an AGY session.
type Resolution struct {
	Session string
	Project string
}

// Binding returns the winning agent and its source ("session", "project", or "none").
func (r Resolution) Binding() (agent string, source string) {
	if r.Session != "" {
		return r.Session, "session"
	}
	if r.Project != "" {
		return r.Project, "project"
	}
	return "", "none"
}

// Resolve resolves both the session binding and the project .conductor-agent file.
func Resolve(root, session, project string) (Resolution, error) {
	var res Resolution
	if session != "" {
		agent, err := binding.Identity(root, AdapterName, session)
		if err != nil {
			return Resolution{}, err
		}
		res.Session = agent
	}
	if project != "" {
		projAgent, err := ResolveProject(project)
		if err != nil {
			return Resolution{}, err
		}
		res.Project = projAgent
	}
	return res, nil
}

// ResolveAgent returns the winning agent identity (session binding first, then project).
func ResolveAgent(root, session, project string) (string, error) {
	res, err := Resolve(root, session, project)
	if err != nil {
		return "", err
	}
	agent, _ := res.Binding()
	return agent, nil
}

// ResolveProject reads the project's .conductor-agent file if present.
func ResolveProject(project string) (string, error) {
	if strings.TrimSpace(project) == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(project, IdentityFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(string(data), "\xef\xbb\xbf")), nil
}

// ProjectDirectory walks upward from startDir looking for a directory containing .conductor-agent.
func ProjectDirectory(startDir string) (string, error) {
	if strings.TrimSpace(startDir) == "" {
		return "", nil
	}
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	cur := abs
	for {
		info, statErr := os.Stat(filepath.Join(cur, IdentityFile))
		if statErr == nil && !info.IsDir() {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}

// HookInput is the camelCase JSON payload Antigravity pipes to hook stdin on
// SessionStart and Stop.
type HookInput struct {
	ConversationID        string   `json:"conversationId"`
	WorkspacePaths        []string `json:"workspacePaths"`
	TranscriptPath        string   `json:"transcriptPath"`
	ArtifactDirectoryPath string   `json:"artifactDirectoryPath"`
	ModelName             string   `json:"modelName"`
	LastUserInput         string   `json:"lastUserInput"`
	ExecutionNum          int      `json:"executionNum"`
	TerminationReason     string   `json:"terminationReason"`
	Error                 string   `json:"error"`
	FullyIdle             *bool    `json:"fullyIdle"`
	// LastAssistant is an optional in-memory override for unit tests; when
	// empty, Stop reads TranscriptPath.
	LastAssistant string `json:"lastAssistant,omitempty"`
}

// ParseHookInput decodes the JSON payload Antigravity passes on stdin. An empty
// stdin returns a zero HookInput.
func ParseHookInput(r io.Reader) (HookInput, error) {
	if r == nil {
		return HookInput{}, nil
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return HookInput{}, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return HookInput{}, nil
	}
	var in HookInput
	if err := json.Unmarshal(trimmed, &in); err != nil {
		return HookInput{}, fmt.Errorf("parse agy hook stdin: %w", err)
	}
	return in, nil
}

// HookSession resolves the conversation ID strictly from the hook payload or an
// explicit --session flag, never falling back to the process environment. This
// prevents a model-started command from forging a hook invocation with empty stdin.
func (in HookInput) HookSession(explicit string) (string, error) {
	session := strings.TrimSpace(explicit)
	if session == "" {
		session = strings.TrimSpace(in.ConversationID)
	}
	if err := binding.Validate(session, ""); err != nil {
		return "", err
	}
	return session, nil
}

// Session resolves the conversation ID from the hook payload, an explicit flag,
// or the process environment.
func (in HookInput) Session(explicit string) (string, error) {
	session := strings.TrimSpace(explicit)
	if session == "" {
		session = strings.TrimSpace(in.ConversationID)
	}
	if session == "" {
		session = strings.TrimSpace(os.Getenv(SessionEnvironment))
	}
	if err := binding.Validate(session, ""); err != nil {
		return "", err
	}
	return session, nil
}

// Project resolves the workspace directory from workspacePaths[0] or the
// process working directory, walking up to any ancestor containing .conductor-agent.
func (in HookInput) Project() (string, error) {
	for _, candidate := range in.WorkspacePaths {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return resolveWorkspaceDirectory(trimmed)
		}
	}
	working, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return resolveWorkspaceDirectory(working)
}

func resolveWorkspaceDirectory(startDir string) (string, error) {
	project, err := ProjectDirectory(startDir)
	if err != nil || project != "" {
		return project, err
	}
	return filepath.Abs(startDir)
}

// InjectedStep is one ephemeral message step injected on SessionStart.
type InjectedStep struct {
	EphemeralMessage string `json:"ephemeralMessage"`
}

// SessionStartOutput is the JSON response Antigravity reads from SessionStart stdout.
type SessionStartOutput struct {
	InjectSteps []InjectedStep `json:"injectSteps,omitempty"`
}

// StopOutput is the JSON response Antigravity reads from Stop stdout.
type StopOutput struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Announcement builds the SessionStart message injected into the model's context.
func Announcement(root, session, project string, resolution Resolution) string {
	agent, source := resolution.Binding()
	if agent == "" {
		return fmt.Sprintf(
			"Conductor's Antigravity adapter is active for this session (session %s), and no agent identity is bound yet (project directory: %s; state root: %s). "+
				"After joining the bus, bind this session with: conductor adapter agy bind <your-agent-name>. "+
				"It binds this session alone, leaves %s untouched, and starts holding your delivery stream across turns.",
			session, project, root, IdentityFile,
		)
	}
	override := ""
	if resolution.Session != "" && resolution.Project != "" && resolution.Session != resolution.Project {
		override = fmt.Sprintf(" (overriding project %s %q)", IdentityFile, resolution.Project)
	}
	residencyNote := "After joining the bus, the adapter will hold your Conductor delivery stream across turns and wake you on Stop when publications arrive."
	if ResidentAlive(root, agent, session) {
		residencyNote = "The adapter holds your Conductor delivery stream across turns and wakes you on Stop when publications arrive."
	}
	return fmt.Sprintf(
		"Conductor's Antigravity adapter is active for this session (session %s): acting as %q (from %s%s; project directory: %s; state root: %s). "+
			"%s "+
			"If this is not the identity you were told to act as, run: conductor adapter agy bind <your-agent-name>.",
		session, agent, source, override, project, root, residencyNote,
	)
}

// RewriteHookCommand substitutes ${CONDUCTOR_BIN} in hooks.json with the OS-specific
// relative binary path inside the installed plugin directory. Antigravity runs
// hooks from hooks.json with cwd set to the plugin root.
func RewriteHookCommand(path string, data []byte) []byte {
	if filepath.ToSlash(path) != "hooks.json" {
		return data
	}
	binary := "./bin/conductor"
	if runtime.GOOS == "windows" || os.PathSeparator == '\\' {
		binary = `bin\\conductor.exe`
	}
	return bytes.ReplaceAll(data, []byte(HookCommandPlaceholder), []byte(binary))
}

// Stop handles Antigravity's Stop hook: it acknowledges a previously presented
// delivery if the session transcript ends with its receipt token, ensures the
// resident stream holder is running, and blocks while idle until a delivery is
// ready to wake the model via {"decision": "continue", "reason": "..."}.
func Stop(ctx context.Context, root string, in HookInput, explicitSession string) (StopOutput, error) {
	if !shouldHandleStopReason(in.TerminationReason) {
		return StopOutput{}, nil
	}
	session, err := in.Session(explicitSession)
	if err != nil {
		return StopOutput{}, err
	}
	project, err := in.Project()
	if err != nil {
		return StopOutput{}, err
	}
	agent, err := ResolveAgent(root, session, project)
	if err != nil || agent == "" {
		return StopOutput{}, err
	}
	client, err := state.Open(root, agent)
	if err != nil {
		return StopOutput{}, err
	}
	status, err := client.WatchStatus()
	if err != nil || !status.Registered {
		return StopOutput{}, err
	}

	waitCtx, cancel := applyWaitTimeout(ctx)
	defer cancel()

	lastAssistant := in.LastAssistant
	if lastAssistant == "" && strings.TrimSpace(in.TranscriptPath) != "" {
		lastAssistant, _ = ExtractLastAssistantText(in.TranscriptPath)
	}

	restartedOnce := false
	for {
		shown, err := loadPresentation(root, agent)
		if err != nil {
			return StopOutput{}, err
		}
		if shown != nil && shown.Session != "" && shown.Session != session {
			if shown.OwnerPID > 0 && processAlive(shown.OwnerPID, shown.OwnerStart) {
				if shown.Claimant != session {
					shown.Claimant = session
					_ = writePresentation(root, agent, *shown)
				}
				return StopOutput{}, nil
			}
			shown.Session = session
			shown.Claimant = ""
			if err := writePresentation(root, agent, *shown); err != nil {
				return StopOutput{}, err
			}
		}
		if shown != nil && shown.Kind == "delivery" && shown.Token != "" {
			if receiptAccepted(lastAssistant, shown.Token) {
				if err := client.AcknowledgeDeliveryReceipt(shown.Receipt); err != nil {
					return StopOutput{}, err
				}
				if err := removePresentation(root, agent); err != nil {
					return StopOutput{}, err
				}
				lastAssistant = ""
				shown = nil
			}
		}
		if shown != nil {
			reason, err := promptFor(root, agent, shown)
			if err != nil {
				return StopOutput{}, err
			}
			return StopOutput{Decision: "continue", Reason: reason}, nil
		}
		if err := EnsureResident(root, session, project); err != nil {
			return StopOutput{}, nil
		}
		shown, err = waitForPresentation(waitCtx, root, agent, session)
		if err != nil {
			return StopOutput{}, err
		}
		if shown == nil {
			if waitCtx.Err() == nil && !restartedOnce && !ResidentAlive(root, agent, session) {
				restartedOnce = true
				continue
			}
			return StopOutput{}, nil
		}
	}
}

func shouldHandleStopReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "", "NO_TOOL_CALL", "end_turn":
		return true
	default:
		return false
	}
}

func applyWaitTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	raw := strings.TrimSpace(os.Getenv(WaitTimeoutEnvironment))
	if raw == "" {
		return context.WithCancel(ctx)
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

func waitForPresentation(ctx context.Context, root, agent, session string) (*presentation, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		shown, err := loadPresentation(root, agent)
		if err != nil {
			return nil, err
		}
		if shown != nil {
			return shown, nil
		}
		if !ResidentAlive(root, agent, session) {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil
		case <-ticker.C:
		}
	}
}

func promptFor(root, agent string, shown *presentation) (string, error) {
	path := presentationPath(root, agent)
	return fmt.Sprintf(
		"Conductor delivery for %s. Read the JSON batch in %s and act on it. If you run no tools in this turn, end your response with this exact final line:\n%s\nIf you run tools, repeat that exact final line at the end of the turn that finishes the delivery.",
		agent, path, shown.Token,
	), nil
}

// ExtractLastAssistantText reads an Antigravity transcript.jsonl file and
// returns the content of the most recent model response step.
func ExtractLastAssistantText(transcriptPath string) (string, error) {
	file, err := os.Open(transcriptPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var last string
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var step struct {
				Source  string `json:"source"`
				Type    string `json:"type"`
				Content string `json:"content"`
				Role    string `json:"role"`
			}
			if json.Unmarshal(trimmed, &step) == nil {
				isModel := step.Type == "PLANNER_RESPONSE" || step.Source == "MODEL" || step.Role == "assistant"
				if isModel && strings.TrimSpace(step.Content) != "" {
					last = step.Content
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return last, readErr
		}
	}
	return last, nil
}

func receiptAccepted(lastAssistant, token string) bool {
	if token == "" {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(lastAssistant, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		return line == token
	}
	return false
}

func receiptToken(receipt state.DeliveryReceipt) (string, error) {
	data, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return receiptPrefix + hex.EncodeToString(sum[:8]), nil
}

// Release stops the resident for the session's bound agent and removes the
// session record.
func Release(root, session, project string) (string, bool, error) {
	agent, err := ResolveAgent(root, session, project)
	if err != nil {
		return "", false, err
	}
	var stopped bool
	if agent != "" {
		stopped, err = stopResident(root, agent, session)
		if err != nil {
			return agent, false, err
		}
	}
	if err := binding.Forget(root, AdapterName, session); err != nil {
		return agent, stopped, err
	}
	return agent, stopped, nil
}

// TakeoverReport describes the two-step confirmation or completed eviction/transfer
// of a held resident or presentation.
type TakeoverReport struct {
	Adapter  string `json:"adapter"`
	Agent    string `json:"agent"`
	Status   string `json:"status"`
	Session  string `json:"session,omitempty"`
	Claimant string `json:"claimant,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Confirm  string `json:"confirm,omitempty"`
}

// Takeover transfers a contested presentation or evicts an existing resident
// for agent using a deterministic two-step confirmation token. Nothing is acknowledged.
func Takeover(root, agent, confirm string) (TakeoverReport, error) {
	shown, err := loadPresentation(root, agent)
	if err != nil {
		return TakeoverReport{}, err
	}
	rec, err := loadResident(root, agent)
	if err != nil {
		return TakeoverReport{}, err
	}
	residentLive := rec != nil && processAlive(rec.PID, rec.ProcessStart)
	if !residentLive && rec != nil {
		_ = removeResident(root, agent)
		rec = nil
	}
	if shown == nil && !residentLive {
		return TakeoverReport{Adapter: AdapterName, Agent: agent, Status: "not-held"}, nil
	}

	var ownerSession, claimant string
	var pid int
	if rec != nil {
		ownerSession = rec.Session
		pid = rec.PID
	}
	if shown != nil {
		if ownerSession == "" {
			ownerSession = shown.Session
		}
		claimant = shown.Claimant
	}
	token := computeTakeoverToken(agent, ownerSession, claimant, pid, shown)
	if strings.TrimSpace(confirm) == "" {
		return TakeoverReport{
			Adapter:  AdapterName,
			Agent:    agent,
			Status:   "confirmation-required",
			Session:  ownerSession,
			Claimant: claimant,
			PID:      pid,
			Confirm:  token,
		}, nil
	}
	if strings.TrimSpace(confirm) != token {
		return TakeoverReport{}, errors.New("takeover confirmation token does not match the current holder; run takeover without --confirm first")
	}
	if rec != nil {
		if _, err := stopResident(root, agent, rec.Session); err != nil {
			return TakeoverReport{}, err
		}
	}
	if shown != nil && shown.Claimant != "" {
		shown.Session = shown.Claimant
		shown.Claimant = ""
		shown.OwnerPID = 0
		shown.OwnerStart = ""
		if err := writePresentation(root, agent, *shown); err != nil {
			return TakeoverReport{}, err
		}
	}
	return TakeoverReport{
		Adapter:  AdapterName,
		Agent:    agent,
		Status:   "released",
		Session:  ownerSession,
		Claimant: claimant,
		PID:      pid,
	}, nil
}

func computeTakeoverToken(agent, session, claimant string, pid int, shown *presentation) string {
	var receiptTok string
	if shown != nil {
		receiptTok = shown.Token
	}
	raw := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", agent, session, claimant, pid, receiptTok)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}

type residentRecord struct {
	Agent        string    `json:"agent"`
	Session      string    `json:"session"`
	PID          int       `json:"pid"`
	ProcessStart string    `json:"process_start,omitempty"`
	HostPID      int       `json:"host_pid,omitempty"`
	HostStart    string    `json:"host_start,omitempty"`
	StartedAt    time.Time `json:"started_at"`
}

type releaseRecord struct {
	Session string    `json:"session"`
	At      time.Time `json:"at"`
}

type presentation struct {
	Kind       string                `json:"kind"`
	Phase      string                `json:"phase"`
	Session    string                `json:"session"`
	Claimant   string                `json:"claimant,omitempty"`
	Token      string                `json:"token,omitempty"`
	Receipt    state.DeliveryReceipt `json:"receipt,omitempty"`
	OwnerPID   int                   `json:"owner_pid,omitempty"`
	OwnerStart string                `json:"owner_start,omitempty"`
	UpdatedAt  time.Time             `json:"updated_at"`
}

func residentPath(root, agent string) string {
	return filepath.Join(binding.Directory(root, AdapterName), "residents", agent+".json")
}

func releaseFlagPath(root, agent string) string {
	return filepath.Join(binding.Directory(root, AdapterName), "releases", agent+".json")
}

func presentationPath(root, agent string) string {
	return filepath.Join(binding.Directory(root, AdapterName), "presentations", agent+".json")
}

func loadResident(root, agent string) (*residentRecord, error) {
	return readJSONFile[residentRecord](residentPath(root, agent))
}

func writeResident(root string, rec residentRecord) error {
	return writeJSONFile(residentPath(root, rec.Agent), rec)
}

func removeResident(root, agent string) error {
	return removeFileWithRetry(residentPath(root, agent))
}

func loadPresentation(root, agent string) (*presentation, error) {
	return readJSONFile[presentation](presentationPath(root, agent))
}

func writePresentation(root, agent string, value presentation) error {
	value.UpdatedAt = time.Now().UTC()
	return writeJSONFile(presentationPath(root, agent), value)
}

func removePresentation(root, agent string) error {
	return removeFileWithRetry(presentationPath(root, agent))
}

func requestRelease(root, agent, session string) error {
	return writeJSONFile(releaseFlagPath(root, agent), releaseRecord{Session: session, At: time.Now().UTC()})
}

func clearRelease(root, agent string) error {
	return removeFileWithRetry(releaseFlagPath(root, agent))
}

func releaseRequested(root, agent, session string) bool {
	rec, err := readJSONFile[releaseRecord](releaseFlagPath(root, agent))
	if err != nil || rec == nil {
		return false
	}
	return rec.Session == "" || rec.Session == session
}

func readJSONFile[T any](path string) (*T, error) {
	var data []byte
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		data, err = os.ReadFile(path)
		if err == nil || errors.Is(err, os.ErrNotExist) || !platform.IsTransientFileAccess(err) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".conductor-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	var replaceErr error
	for attempt := 0; attempt < 20; attempt++ {
		replaceErr = platform.ReplaceFile(tempPath, path)
		if replaceErr == nil || !platform.IsTransientFileAccess(replaceErr) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return replaceErr
}

func removeFileWithRetry(path string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		err = os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) || !platform.IsTransientFileAccess(err) {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return err
}
