package ask

import "time"

const (
	StatusPendingApproval = "pending_approval"
	StatusApproved        = "approved"
	StatusRunning         = "running"
	StatusAnswered        = "answered"
	StatusDenied          = "denied"
	StatusFailed          = "failed"
	StatusTimedOut        = "timed_out"
)

type Request struct {
	ID          string             `json:"id"`
	FromSession string             `json:"from_session"`
	ToSession   string             `json:"to_session"`
	FromPath    string             `json:"from_path,omitempty"`
	ToPath      string             `json:"to_path,omitempty"`
	Question    string             `json:"question"`
	Status      string             `json:"status"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	Response    *Response          `json:"response,omitempty"`
	Execution   *ExecutionMetadata `json:"execution,omitempty"`
	Error       string             `json:"error,omitempty"`
}

type Response struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	Responder string    `json:"responder"`
}

type ExecutionMetadata struct {
	Command    string     `json:"command,omitempty"`
	PromptPath string     `json:"prompt_path,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   int        `json:"exit_code,omitempty"`
	LogPath    string     `json:"log_path,omitempty"`
}

type Approval struct {
	FromSession string    `json:"from_session"`
	ToSession   string    `json:"to_session"`
	FromPath    string    `json:"from_path"`
	ToPath      string    `json:"to_path"`
	CreatedAt   time.Time `json:"created_at"`
}

type ApprovalStore struct {
	Approvals []Approval `json:"approvals"`
}

type Policy struct {
	Enabled  bool
	Mode     string
	Command  string
	Args     []string
	Timeout  time.Duration
	ReadOnly bool
}
