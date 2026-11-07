package handoff

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// JSONBoardPolicy selects an optional read-only source, never a native board
// provider. The entire configuration is part of the existing policy digest.
// Context is an explicit trusted correlation for boards without inline routing
// fields; callers cannot supply missing authority through Request.
type JSONBoardPolicy struct {
	Schema   string             `json:"schema"`
	Context  JSONBoardContext   `json:"context"`
	Elements []JSONBoardElement `json:"elements"`
}

type JSONBoardContext struct {
	Project     string `json:"project"`
	ProjectRoot string `json:"project_root"`
	BoardDir    string `json:"board_dir"`
	RoomAlias   string `json:"room_alias"`
	RoomID      string `json:"room_id"`
	Topic       string `json:"topic"`
	ReplyTo     string `json:"reply_to"`
}

type JSONBoardElement struct {
	ID         string `json:"id"`
	File       string `json:"file"`
	SHA256     string `json:"sha256"`
	AssignedTo string `json:"assigned_to"`
}

// keeperAssignmentV1 is a closed on-disk schema, with no routing defaults and
// no executable fields. A changed board shape requires a new source contract.
type keeperAssignmentV1 struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	AccountableOwner string `json:"accountable_owner"`
	AssignedTo       string `json:"assigned_to"`
	AuthorityEvent   string `json:"authority_event"`
	CreatedAt        string `json:"created_at"`
	Scope            string `json:"scope"`
	Status           string `json:"status"`
}

func validateJSONBoardPolicy(p Policy, ids assignmentIDMatcher) string {
	b := p.JSONBoard
	if b == nil {
		return ""
	}
	if b.Schema != "keeper-assignment/1" {
		return "board_schema_not_supported"
	}
	c := b.Context
	for _, field := range []struct{ name, value string }{
		{"project", c.Project}, {"project_root", c.ProjectRoot}, {"board_dir", c.BoardDir},
		{"room_alias", c.RoomAlias}, {"room_id", c.RoomID}, {"topic", c.Topic}, {"reply_to", c.ReplyTo},
	} {
		if field.value == "" {
			return "board_context_" + field.name + "_missing"
		}
	}
	if c.Project != p.Project || c.ProjectRoot != p.ProjectRoot || c.BoardDir != p.BoardDir || c.RoomAlias != "#"+p.Room || c.RoomID != p.RoomID || c.Topic != p.Topic || c.ReplyTo != p.ReplyTo {
		return "board_context_mismatch"
	}
	// The board must be strictly beneath the admitted project root. No path in
	// the request, environment or board document participates in file selection.
	if p.ProjectRoot == string(os.PathSeparator) || len(p.ProjectRoot) > 4096 || len(p.BoardDir) > 4096 || strings.Count(p.BoardDir, string(os.PathSeparator)) > 128 {
		return "board_path_not_contained"
	}
	rel, err := filepath.Rel(p.ProjectRoot, p.BoardDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "board_path_not_contained"
	}
	if len(b.Elements) == 0 || len(b.Elements) > 256 || len(b.Elements) != len(p.Tasks) {
		return "board_elements_invalid"
	}
	tasks := map[string]TaskBinding{}
	for _, task := range p.Tasks {
		tasks[task.ID] = task
	}
	seen := map[string]bool{}
	for _, element := range b.Elements {
		task, ok := tasks[element.ID]
		digest, err := hex.DecodeString(element.SHA256)
		// NAME_MAX includes the extension, even when the configured ID bound
		// admits a longer bare ID for a different provider.
		if !ok || !ids.matches(element.ID) || len(element.File) > 255 || element.File != element.ID+".json" || seen[element.ID] || err != nil || len(digest) != sha256.Size || strings.ToLower(element.SHA256) != element.SHA256 || !name.MatchString(element.AssignedTo) || len(task.Members) != 1 || task.Members[0] != element.AssignedTo {
			return "board_elements_invalid"
		}
		seen[element.ID] = true
	}
	return ""
}

// openBoardDirectory walks from a directory FD with O_NOFOLLOW at every step,
// avoiding path-check/open races and symlink escapes on all supported platforms.
// Custody, including platform ACLs, is checked on the same opened object used
// for the next relative open or read; no check reopens an object by its path.
// Same-user trusted ancestors are assumed, as for the existing private policy.
func openBoardDirectory(p Policy) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), "/")
	path := ""
	for _, component := range strings.Split(strings.TrimPrefix(p.BoardDir, "/"), "/") {
		path += "/" + component
		next, err := unix.Openat(int(current.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		current.Close()
		if err != nil {
			return nil, err
		}
		current = os.NewFile(uintptr(next), component)
		if path == p.ProjectRoot || path == p.BoardDir {
			var st unix.Stat_t
			if unix.Fstat(next, &st) != nil || st.Uid != uint32(os.Getuid()) || st.Mode&0022 != 0 || !noExtendedACL(current) {
				current.Close()
				return nil, errors.New("untrusted board directory")
			}
		}
	}
	return current, nil
}

func readBoardElement(p Policy, element JSONBoardElement) ([]byte, error) {
	dir, err := openBoardDirectory(p)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), element.File, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), element.File)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Mode&0022 != 0 || st.Nlink != 1 || st.Size > FileLimit || !noExtendedACL(f) {
		return nil, errors.New("invalid board file")
	}
	data, err := io.ReadAll(io.LimitReader(f, FileLimit+1))
	if err != nil || len(data) > FileLimit {
		return nil, errors.New("board file too large")
	}
	return data, nil
}

func validateJSONBoardSource(r Request, p Policy) string {
	// Called only after common validation, including the closed policy seam.
	var selected *JSONBoardElement
	for i := range p.JSONBoard.Elements {
		if p.JSONBoard.Elements[i].ID == r.TaskID {
			selected = &p.JSONBoard.Elements[i]
		}
	}
	if selected == nil {
		return "board_element_not_bound"
	}
	data, err := readBoardElement(p, *selected)
	if err != nil {
		return "board_element_unavailable"
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != selected.SHA256 {
		return "board_element_changed"
	}
	var task keeperAssignmentV1
	if Decode(data, &task) != nil {
		return "board_element_malformed"
	}
	if task.ID == "" || task.Title == "" || task.AccountableOwner == "" || task.AssignedTo == "" || task.AuthorityEvent == "" || task.CreatedAt == "" || task.Scope == "" || task.Status == "" {
		return "board_element_fields_missing"
	}
	if !textOK(task.Title) || !name.MatchString(task.AccountableOwner) || !name.MatchString(task.AssignedTo) || !eventID.MatchString(task.AuthorityEvent) || !textOK(task.Scope) {
		return "board_element_malformed"
	}
	if _, err := time.Parse(time.RFC3339Nano, task.CreatedAt); err != nil {
		return "board_element_malformed"
	}
	if task.ID != r.TaskID || task.Title != r.TaskTitle || task.AccountableOwner != p.Sender || task.AssignedTo != r.Member || task.AssignedTo != selected.AssignedTo || task.Status != "ASSIGNED" {
		return "board_element_mismatch"
	}
	return ""
}
