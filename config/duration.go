package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	s := string(b)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return fmt.Errorf("invalid duration %q", s)
		}
		*d = Duration(time.Duration(n) * 24 * time.Hour)
		return nil
	}

	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}

	*d = Duration(dur)
	return nil
}
