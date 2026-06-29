package core

import "time"

// ProjectConfig is the persisted shape of .ask/config.json. ProjectID is a
// ULID generated at `ask init` time; DisplayName defaults to the basename
// of the project root but is user-overridable.
type ProjectConfig struct {
	ProjectID   string    `json:"project_id"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}
