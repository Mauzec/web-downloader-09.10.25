package utils

import "time"

func TimePtr(t time.Time) *time.Time {
	tt := t
	return &tt
}
