package user

import "testing"

func TestPublicProfileNeedsFollow(t *testing.T) {
	tests := []struct {
		name             string
		viewer           string
		target           string
		private          bool
		recruiterVisible bool
		want             bool
	}{
		{"owner can view private profile", "owner", "owner", true, false, false},
		{"private profile requires relationship", "viewer", "owner", true, false, true},
		{"public profile needs no relationship", "viewer", "owner", false, false, false},
		{"recruiter visibility also opens public view", "viewer", "owner", true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := publicProfileNeedsFollow(tt.viewer, tt.target, tt.private, tt.recruiterVisible); got != tt.want {
				t.Fatalf("publicProfileNeedsFollow() = %v, want %v", got, tt.want)
			}
		})
	}
}
