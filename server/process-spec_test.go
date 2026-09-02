package server

import (
	"errors"
	"testing"
)

func TestProcessSpecValidate(t *testing.T) {
	cases := []struct {
		name string
		spec ProcessSpec
		want error
	}{
		{"zero generation", ProcessSpec{Port: 9000, SpoolDir: "/s"}, ErrInvalidSpec},
		{"zero port", ProcessSpec{Generation: 1, SpoolDir: "/s"}, ErrInvalidSpec},
		{"port too high", ProcessSpec{Generation: 1, Port: 70000, SpoolDir: "/s"}, ErrInvalidSpec},
		{"empty spool", ProcessSpec{Generation: 1, Port: 9000}, ErrInvalidSpec},
		{"valid", ProcessSpec{Generation: 1, Port: 9000, SpoolDir: "/s"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if tc.want == nil {
				if err != nil {
					t.Fatal(err.Error())
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}
