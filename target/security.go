package target

// SecurityOpts holds container security configuration.
type SecurityOpts struct {
	MemoryLimit  string // e.g. "4g"
	CPULimit     string // e.g. "2"
	PidsLimit    int
	ReadOnlyRoot bool
	CapDrop      []string // capabilities to drop
	NoNewPrivs   bool
	TmpfsMounts  []string // tmpfs paths when read-only root is on
}

// DefaultSecurityOpts returns sane security defaults for Docker containers.
func DefaultSecurityOpts() SecurityOpts {
	return SecurityOpts{
		MemoryLimit: "4g",
		CPULimit:    "4",
		PidsLimit:   512,
		CapDrop:     []string{"ALL"},
		NoNewPrivs:  true,
	}
}
