package admin

import "testing"

func TestIsOwner(t *testing.T) {
	tests := []struct {
		name    string
		ownerID int64
		userID  int64
		want    bool
	}{
		{name: "owner", ownerID: 42, userID: 42, want: true},
		{name: "different user", ownerID: 42, userID: 43, want: false},
		{name: "invalid owner configuration", ownerID: 0, userID: 0, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsOwner(test.ownerID, test.userID); got != test.want {
				t.Errorf("IsOwner(%d, %d) = %v, want %v", test.ownerID, test.userID, got, test.want)
			}
		})
	}
}
