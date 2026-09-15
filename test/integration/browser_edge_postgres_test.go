package integration

import (
	"context"
	"testing"

	"travelmcp/test/common"
)

func TestBrowserEdgeContractPostgres(t *testing.T) {
	pool := mergePool(t)
	ps := mergeStore(t, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM terminals WHERE id IN (SELECT terminal_id FROM terminal_names WHERE name LIKE 'ZZEDGE9Q%')`)
	})
	common.RunBrowserEdgeContract(t, ps)
}
