package tentaclepost_test

import (
	"os"
	"strings"
	"testing"
)

func TestLookoutGateTargetsOnlyLabeledFindings(t *testing.T) {
	source, err := os.ReadFile(".github/workflows/reliability-lookout.md")
	if err != nil {
		t.Fatal(err)
	}
	frontmatter := strings.SplitN(string(source), "\n---", 2)[0]
	for _, required := range []string{
		"  skip-if-match: 'is:issue is:open label:reliability-finding'\n",
		"    labels: [reliability-finding]\n",
		"    allowed-labels: [reliability-finding]\n",
		"gh issue list --state all --label reliability-finding --limit 20 ",
	} {
		if !strings.Contains(frontmatter, required) {
			t.Errorf("workflow must consistently identify real findings: missing %q", required)
		}
	}
}
