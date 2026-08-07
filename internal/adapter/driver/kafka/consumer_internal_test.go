package kafka

import (
	"errors"
	"fmt"
	"testing"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// TestIsTransientFetchError locks in the exact classification that fixes
// the bug found in review: Consume used to treat every fetch error as
// fatal, including franz-go's own documented informational classes that
// the client already recovers from internally. Without this distinction, a
// transient *kgo.ErrDataLoss or *kgo.ErrGroupSession kills the whole
// worker process on a single-node broker blip, with no restart policy in
// docker-compose.yml to bring it back.
func TestIsTransientFetchError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "ErrDataLoss is transient — client already reset and resumed",
			err:  &kgo.ErrDataLoss{Topic: "t", Partition: 0, ConsumedTo: 10, ResetTo: 5},
			want: true,
		},
		{
			name: "ErrGroupSession is transient — informational group-session loss",
			err:  &kgo.ErrGroupSession{Err: errors.New("kicked from group")},
			want: true,
		},
		{
			name: "ErrDataLoss wrapped by a caller is still recognized via errors.As semantics",
			err:  fmt.Errorf("fetch: %w", &kgo.ErrDataLoss{Topic: "t", Partition: 1}),
			want: true,
		},
		{
			name: "a plain kerr.Error stays fatal",
			err:  kerr.UnknownTopicOrPartition,
			want: false,
		},
		{
			name: "an untyped error stays fatal",
			err:  errors.New("batch parse failure"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientFetchError(tc.err); got != tc.want {
				t.Errorf("isTransientFetchError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
