//go:build !linux

package terminal

import (
	"errors"
	"os"
)

type State struct{}

func Width(*os.File) int { return 0 }

func MakeRaw(*os.File) (*State, error) {
	return nil, errors.New("interactive input is not supported on this platform")
}
func (*State) Restore() {}
