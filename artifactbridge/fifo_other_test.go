//go:build !(darwin || linux)

package artifactbridge

import "errors"

func mkfifo(string) error { return errors.New("unsupported") }
