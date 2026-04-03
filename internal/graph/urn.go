package graph

import (
	"fmt"
	"strings"
)

// URN is the stable, non-content-addressed identity of every node and relation.
// Format: urn:moos:<type>:<qualifier>
// CI-3: urn(M(x)) = urn(x) — identity never changes under any rewrite.
type URN string

func (u URN) String() string { return string(u) }

func (u URN) Validate() error {
	s := string(u)
	if !strings.HasPrefix(s, "urn:moos:") {
		return fmt.Errorf("invalid URN %q: must start with urn:moos:", s)
	}
	parts := strings.SplitN(s, ":", 4)
	if len(parts) < 4 || parts[3] == "" {
		return fmt.Errorf("invalid URN %q: must have at least 4 colon-separated parts", s)
	}
	return nil
}
