package memory

import (
	"testing"

	"travelmcp/test/common"
)

func TestBrowserEdgeContract(t *testing.T) {
	common.RunBrowserEdgeContract(t, NewMemoryStore())
}
