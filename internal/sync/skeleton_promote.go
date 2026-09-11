package sync

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"strings"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	store "travelmcp/internal/store"
)

type SkeletonStore interface {
	UpsertTerminal(ctx context.Context, r store.TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error)
	ListTerminalIDByCode(ctx context.Context, system, code string) (int64, bool)
	SetTerminalTag(ctx context.Context, id int64, key, value string) error
	UpsertTerminalAlias(ctx context.Context, a store.TerminalAliasRow) error
	UpsertAttributeState(ctx context.Context, a store.AttributeStateRow) error
	SaveProvenance(ctx context.Context, p model.Provenance) error
	SaveReviewQueue(ctx context.Context, e model.ReviewQueueEntry) error
	CreateSyncRun(ctx context.Context, r store.SyncRunRow) (int64, error)
	FinishSyncRun(ctx context.Context, id int64, state, summary string) error
}

type SkeletonChunk struct {
	Key        string
	Canon      []skeleton.JoinedRecord
	Unverified []model.AdaptedRecord
	Ambiguous  []skeleton.JoinedRecord
}

type ChunkSummary struct {
	Key      string
	In       int
	Written  int
	Review   int
	Enriched int
}

type RunSummary struct {
	In        int `json:"in"`
	Written   int `json:"written"`
	Review    int `json:"review"`
	Enriched  int `json:"enriched"`
	Chunks    int `json:"chunks"`
	Unmatched int `json:"unmatched"`
}

func ChunkSkeleton(outcome skeleton.JoinOutcome, size int) []SkeletonChunk {
	if size <= 0 || size > 100 {
		size = 100
	}
	canon := append([]skeleton.JoinedRecord{}, outcome.Canon...)
	sort.Slice(canon, func(i, j int) bool {
		return skeletonChunkLess(canon[i].Record, canon[j].Record)
	})
	unverified := append([]model.AdaptedRecord{}, outcome.Unverified...)
	sort.Slice(unverified, func(i, j int) bool {
		return skeletonChunkLess(unverified[i], unverified[j])
	})
	var chunks []SkeletonChunk
	for start := 0; start < len(canon); start += size {
		end := start + size
		if end > len(canon) {
			end = len(canon)
		}
		part := canon[start:end]
		chunks = append(chunks, SkeletonChunk{
			Key:   skeletonChunkKey(part[0].Record, part[len(part)-1].Record, len(chunks)),
			Canon: part,
		})
	}
	for start := 0; start < len(unverified); start += size {
		end := start + size
		if end > len(unverified) {
			end = len(unverified)
		}
		part := unverified[start:end]
		region := recordRegion(part[0])
		if region == "" {
			region = "unknown"
		}
		chunks = append(chunks, SkeletonChunk{
			Key:        fmt.Sprintf("review:%s:%d", region, len(chunks)),
			Unverified: part,
		})
	}
	ambiguous := append([]skeleton.JoinedRecord{}, outcome.DuplicateAmbiguous...)
	sort.Slice(ambiguous, func(i, j int) bool {
		return skeletonChunkLess(ambiguous[i].Record, ambiguous[j].Record)
	})
	for start := 0; start < len(ambiguous); start += size {
		end := start + size
		if end > len(ambiguous) {
			end = len(ambiguous)
		}
		part := ambiguous[start:end]
		region := recordRegion(part[0].Record)
		if region == "" {
			region = "unknown"
		}
		chunks = append(chunks, SkeletonChunk{
			Key:       fmt.Sprintf("ambiguous:%s:%d", region, len(chunks)),
			Ambiguous: part,
		})
	}
	return chunks
}

func skeletonChunkLess(a, b model.AdaptedRecord) bool {
	if ra, rb := recordRegion(a), recordRegion(b); ra != rb {
		return ra < rb
	}
	if ta, tb := recordTransport(a), recordTransport(b); ta != tb {
		return ta < tb
	}
	if ca, cb := a.PrimaryCode(), b.PrimaryCode(); ca != cb {
		return ca < cb
	}
	return a.NameRu < b.NameRu
}

func skeletonChunkKey(first, last model.AdaptedRecord, idx int) string {
	region := recordRegion(first)
	if region == "" {
		region = "unknown"
	}
	transport := recordTransport(first)
	if transport == "" {
		transport = "any"
	}
	return fmt.Sprintf("terminals:%s:%s:%s-%s", region, transport, first.PrimaryCode(), last.PrimaryCode()+fmt.Sprintf(":%d", idx))
}

func recordRegion(r model.AdaptedRecord) string {
	if r.Extra == nil {
		return ""
	}
	return r.Extra["region"]
}

func recordTransport(r model.AdaptedRecord) string {
	if r.Extra == nil {
		return ""
	}
	return r.Extra["transport_type"]
}

func BeginSkeletonRun(ctx context.Context, st SkeletonStore, planID, inputSHA, tag string) (int64, error) {
	return st.CreateSyncRun(ctx, store.SyncRunRow{PlanID: planID, Kind: "skeleton", InputSHA: inputSHA, Tag: tag})
}

func FinishSkeletonRun(ctx context.Context, st SkeletonStore, runID int64, state string, summary RunSummary) error {
	if err := CheckRunBalance(summary); err != nil {
		return err
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	return st.FinishSyncRun(ctx, runID, state, string(raw))
}

func CheckRunBalance(s RunSummary) error {
	if s.In != s.Written+s.Review {
		return fmt.Errorf("sync skeleton: несходимость строк: in=%d written=%d review=%d", s.In, s.Written, s.Review)
	}
	return nil
}

func PromoteSkeletonChunk(ctx context.Context, st store.Store, runID int64, chunk SkeletonChunk) (ChunkSummary, error) {
	if _, ok := st.(SkeletonStore); !ok {
		return ChunkSummary{}, fmt.Errorf("sync skeleton: стор не умеет промоушен скелета (нет алиасов/attribute_state)")
	}
	sum := ChunkSummary{Key: chunk.Key, In: len(chunk.Canon) + len(chunk.Unverified) + len(chunk.Ambiguous)}
	err := st.WithTx(ctx, func(tx store.Store) error {
		tskel, ok := tx.(SkeletonStore)
		if !ok {
			return fmt.Errorf("sync skeleton: транзакция не умеет промоушен скелета")
		}
		for _, j := range chunk.Canon {
			if err := ctx.Err(); err != nil {
				return err
			}
			written, err := promoteJoined(ctx, tskel, runID, j)
			if err != nil {
				return err
			}
			if written {
				sum.Written++
				if j.Enrichment == skeleton.Enriched {
					sum.Enriched++
				}
			} else {
				sum.Review++
			}
		}
		for _, r := range chunk.Unverified {
			if err := ctx.Err(); err != nil {
				return err
			}
			if r.HasCoords() {
				// unverifiedPromoteScore — confidence unverified-канона (O-10):
				// ниже verified-порога, выше seed-уровня интерполяции.
				const unverifiedPromoteScore = 0.4
				written, err := promoteJoined(ctx, tskel, runID, skeleton.JoinedRecord{
					Record:     r,
					Score:      unverifiedPromoteScore,
					Enrichment: skeleton.IdentityOnly,
				})
				if err != nil {
					return err
				}
				if written {
					sum.Written++
				}
				continue
			}
			if err := reviewUnverified(ctx, tskel, r); err != nil {
				return err
			}
			sum.Review++
		}
		for _, j := range chunk.Ambiguous {
			if err := ctx.Err(); err != nil {
				return err
			}
			r := j.Record
			if err := tskel.SaveReviewQueue(ctx, model.ReviewQueueEntry{
				EntityType:  "terminal",
				EntityID:    syntheticReviewID("terminal", "duplicate_ambiguous", r.Source, fallbackCode(r)),
				Reason:      "duplicate_ambiguous",
				Score:       j.Score,
				Fingerprint: externalFingerprint(r),
			}); err != nil {
				return err
			}
			sum.Review++
		}
		return nil
	})
	if err != nil {
		return ChunkSummary{}, err
	}
	if sum.In != sum.Written+sum.Review {
		return ChunkSummary{}, fmt.Errorf("sync skeleton: несходимость чанка %s: in=%d written=%d review=%d", chunk.Key, sum.In, sum.Written, sum.Review)
	}
	slog.Info("sync skeleton: чанк промоутнут", "run_id", runID, "chunk", chunk.Key, "written", sum.Written, "review", sum.Review)
	return sum, nil
}

func promoteJoined(ctx context.Context, st SkeletonStore, runID int64, j skeleton.JoinedRecord) (bool, error) {
	r := j.Record
	if !r.HasCoords() {
		if err := st.SaveReviewQueue(ctx, model.ReviewQueueEntry{
			EntityType:  "terminal",
			EntityID:    syntheticReviewID("terminal", "low_confidence", r.Source, fallbackCode(r)),
			Reason:      "low_confidence",
			Score:       j.Score,
			Fingerprint: externalFingerprint(r),
		}); err != nil {
			return false, err
		}
		return false, nil
	}
	row := store.TerminalRow{
		Lat:              *r.Lat,
		Lon:              *r.Lon,
		Tz:               r.Tz,
		TransportTypes:   splitTransport(recordTransport(r)),
		ObjectType:       recordObjectType(r),
		EnrichmentStatus: string(j.Enrichment),
	}
	if v := recordExtra(r, "address"); v != "" {
		row.Address = v
	}
	// Primary-имя: yandex-title информативнее голого OSM-имени, когда
	// добавляет город-контекст («Кемерово, автовокзал» vs «Автовокзал»).
	// OSM-имя при этом сохраняется alias-ом, ничего не теряется.
	nameRu := r.NameRu
	if y := recordExtra(r, "yandex_title"); y != "" && yandexNameAddsContext(y, r.NameRu) {
		nameRu = y
	}
	names := map[string]string{"ru": nameRu}
	if r.NameEn != "" {
		names["en"] = r.NameEn
	}
	// Дедуп по внешнему коду: повторный прогон промоутит существующий
	// терминал (идемпотентный UPDATE), а не создаёт дубликат. Иначе
	// UNIQUE(system, code) молча отнимает идентификатор у новой записи.
	for _, id := range r.Identifiers {
		if existing, ok := st.ListTerminalIDByCode(ctx, id.System, id.Code); ok {
			row.ID = existing
			break
		}
	}
	id, err := st.UpsertTerminal(ctx, row, names, r.Identifiers)
	if err != nil {
		return false, err
	}
	if v := recordExtra(r, "yandex_title"); v != "" {
		if err := st.UpsertTerminalAlias(ctx, store.TerminalAliasRow{TerminalID: id, Alias: v, Lang: "ru", Source: "yandex"}); err != nil {
			return false, err
		}
	}
	if nameRu != r.NameRu {
		if err := st.UpsertTerminalAlias(ctx, store.TerminalAliasRow{TerminalID: id, Alias: r.NameRu, Lang: "ru", Source: "osm"}); err != nil {
			return false, err
		}
	}
	if v := recordExtra(r, "settlement"); v != "" {
		if err := st.SetTerminalTag(ctx, id, "settlement", v); err != nil {
			return false, err
		}
	}
	geomSource := geomWinnerSource(r)
	now := time.Now()
	syncID := &runID
	for _, attr := range []store.AttributeStateRow{
		{EntityType: "terminal", EntityID: id, Field: "geom", Value: fmt.Sprintf("%v,%v", *r.Lat, *r.Lon), Source: geomSource, Confidence: j.Score, Origin: "live", SyncRunID: syncID},
		{EntityType: "terminal", EntityID: id, Field: "name_ru", Value: nameRu, Source: geomSource, Confidence: j.Score, Origin: "live", SyncRunID: syncID},
	} {
		if err := st.UpsertAttributeState(ctx, attr); err != nil {
			return false, err
		}
	}
	for _, src := range provenanceSources(r, j.Enrichment == skeleton.Enriched) {
		if err := st.SaveProvenance(ctx, model.Provenance{EntityType: "terminal", EntityID: id, Source: src, Confidence: j.Score, ObservedAt: now, Channel: model.ChannelLocalFile}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func reviewUnverified(ctx context.Context, st SkeletonStore, r model.AdaptedRecord) error {
	return st.SaveReviewQueue(ctx, model.ReviewQueueEntry{
		EntityType:  "terminal",
		EntityID:    syntheticReviewID("terminal", "skeleton_unverified", r.Source, fallbackCode(r)),
		Reason:      "skeleton_unverified",
		Score:       0,
		Fingerprint: externalFingerprint(r),
	})
}

func geomWinnerSource(r model.AdaptedRecord) string {
	for _, id := range r.Identifiers {
		if id.System == "osm" {
			return "osm"
		}
	}
	if r.Source != "" {
		return r.Source
	}
	return "osm"
}

func provenanceSources(r model.AdaptedRecord, enriched bool) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if enriched {
		add("osm")
		add("yandex")
		return out
	}
	for _, id := range r.Identifiers {
		add(id.System)
	}
	add(r.Source)
	return out
}

func recordExtra(r model.AdaptedRecord, key string) string {
	if r.Extra == nil {
		return ""
	}
	return r.Extra[key]
}

// yandexNameAddsContext — yandex-имя информативнее OSM-имени как primary,
// если добавляет контекст (город-префикс), а OSM-имя целиком вложено в
// yandex-имя как «хвост»: «Кемерово, автовокзал» ⊇ «Автовокзал»,
// «Барнаул» ⊇ «Барнаул». Короткие OSM-имена без контекста («Вокзал»,
// «Автовокзал», «Автостанция») — типичный кейс остановок у транспортных
// хабов, их OSM-имя бесполезно без города.
func yandexNameAddsContext(yandexName, osmName string) bool {
	if yandexName == "" || osmName == "" {
		return false
	}
	y := strings.ToLower(strings.TrimSpace(yandexName))
	o := strings.ToLower(strings.TrimSpace(osmName))
	if y == o || !strings.HasSuffix(y, o) {
		return false
	}
	prefix := strings.TrimSpace(strings.TrimSuffix(y, o))
	prefix = strings.TrimSuffix(strings.TrimSuffix(prefix, ","), " ")
	return prefix != ""
}

func recordObjectType(r model.AdaptedRecord) string {
	if v := recordExtra(r, "station_type"); v != "" {
		return v
	}
	return recordExtra(r, "object_type")
}

func splitTransport(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "+")
}

func fallbackCode(r model.AdaptedRecord) string {
	if c := r.PrimaryCode(); c != "" {
		return c
	}
	return r.NameRu
}

func externalFingerprint(r model.AdaptedRecord) string {
	return r.Source + ":" + fallbackCode(r)
}

func syntheticReviewID(entityType, reason, source, code string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(entityType + "\x00" + reason + "\x00" + source + "\x00" + code))
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], h.Sum64())
	v := int64(binary.LittleEndian.Uint64(b[:]))
	if v >= 0 {
		v = -v - 1
	}
	if v == 0 {
		v = -1
	}
	return v
}
