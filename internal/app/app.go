package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
)

type App struct {
	abci.BaseApplication

	mu      sync.RWMutex
	state   map[string][]byte // ns|key -> value
	height  int64
	appHash []byte
}

func NewApp() *App { return &App{state: make(map[string][]byte)} }

// 인터페이스 만족 여부 (컴파일 타임 체크)
var _ abci.Application = (*App)(nil)

// -----------------------------
// ABCI 2.0 hooks
// -----------------------------

func (a *App) Info(ctx context.Context, req *abci.InfoRequest) (*abci.InfoResponse, error) {
	return &abci.InfoResponse{
		Data:             "optimistic-parallel-execution-poc",
		Version:          "0.1.0",
		AppVersion:       1,
		LastBlockHeight:  a.height,
		LastBlockAppHash: append([]byte(nil), a.appHash...),
	}, nil
}

func (a *App) InitChain(ctx context.Context, req *abci.InitChainRequest) (*abci.InitChainResponse, error) {
	a.height = 0
	a.appHash = nil
	return &abci.InitChainResponse{}, nil
}

func (a *App) PrepareProposal(ctx context.Context, req *abci.PrepareProposalRequest) (*abci.PrepareProposalResponse, error) {
	// 초기 버전: 결정성 보장을 위해 원순서 유지
	return &abci.PrepareProposalResponse{Txs: req.Txs}, nil
}

func (a *App) ProcessProposal(ctx context.Context, req *abci.ProcessProposalRequest) (*abci.ProcessProposalResponse, error) {
	return &abci.ProcessProposalResponse{Status: abci.PROCESS_PROPOSAL_STATUS_ACCEPT}, nil
}

func (a *App) CheckTx(ctx context.Context, req *abci.CheckTxRequest) (*abci.CheckTxResponse, error) {
	// 최소 구문검사: put|<ns>|<key>|<value>
	if _, err := parseTx(req.Tx); err != nil {
		return &abci.CheckTxResponse{Code: 1, Info: err.Error()}, nil
	}
	return &abci.CheckTxResponse{Code: 0}, nil
}

func (a *App) FinalizeBlock(ctx context.Context, req *abci.FinalizeBlockRequest) (*abci.FinalizeBlockResponse, error) {
	start := time.Now()

	// 결과는 반드시 블록 TX 개수와 동일한 길이
	results := make([]*abci.ExecTxResult, len(req.Txs))

	// 1) 파싱(+원래 인덱스 저장)
	txs := make([]Tx, 0, len(req.Txs))
	for i, raw := range req.Txs {
		t, err := parseTx(raw)
		if err != nil {
			results[i] = &abci.ExecTxResult{Code: 1, Log: "bad tx format"}
			continue
		}
		t.Idx = i
		txs = append(txs, t)
	}

	// 2) 키스페이스별 그룹핑
	buckets := groupByNS(txs)

	// 3) NS 단위 병렬 실행(충돌 시 NS 내부 순차 폴백)
	var wg sync.WaitGroup
	mu := &a.mu
	st := a.state

	for ns := range buckets {
		ns := ns
		wg.Add(1)
		go func() {
			defer wg.Done()

			writes := make(map[string][]byte)
			order := make([]Tx, 0, len(buckets[ns]))
			dup := false

			for _, t := range buckets[ns] {
				if t.Op != "put" {
					results[t.Idx] = &abci.ExecTxResult{Code: 2, Log: "unsupported op"}
					continue
				}
				k := ns + "|" + t.K
				if _, ok := writes[k]; ok {
					dup = true
					break
				}
				writes[k] = t.V
				order = append(order, t)
			}

			if dup {
				// 충돌: NS 전체 순차 적용
				mu.Lock()
				for _, t := range buckets[ns] {
					if t.Op == "put" {
						st[t.NS+"|"+t.K] = t.V
						results[t.Idx] = &abci.ExecTxResult{Code: 0}
					} else {
						results[t.Idx] = &abci.ExecTxResult{Code: 2, Log: "unsupported op"}
					}
				}
				mu.Unlock()
				return
			}

			// 충돌 없음: 일괄 커밋
			mu.Lock()
			for k, v := range writes {
				st[k] = v
			}
			for _, t := range order {
				results[t.Idx] = &abci.ExecTxResult{Code: 0}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	// 누락 보호 (이상 케이스)
	for i := range results {
		if results[i] == nil {
			results[i] = &abci.ExecTxResult{Code: 0}
		}
	}

	a.height++

	// AppHash 계산 및 보관(Info용)
	appHash := computeAppHash(st)
	a.appHash = append([]byte(nil), appHash...)

	_ = time.Since(start)

	return &abci.FinalizeBlockResponse{
		TxResults: results, // ← 길이 = len(req.Txs)
		AppHash:   appHash,
	}, nil
}

func (a *App) Commit(ctx context.Context, req *abci.CommitRequest) (*abci.CommitResponse, error) {
	// ABCI 2.0: AppHash는 FinalizeBlock에서 반환됨
	return &abci.CommitResponse{}, nil
}

func (a *App) Query(ctx context.Context, req *abci.QueryRequest) (*abci.QueryResponse, error) {
	// 간단 조회: path="/kv", data="ns|key"
	if string(req.Path) != "/kv" {
		return &abci.QueryResponse{Code: 1, Info: "unsupported path"}, nil
	}
	a.mu.RLock()
	v, ok := a.state[string(req.Data)]
	a.mu.RUnlock()
	if !ok {
		return &abci.QueryResponse{Code: 0, Value: nil}, nil
	}
	return &abci.QueryResponse{Code: 0, Value: append([]byte(nil), v...)}, nil
}

// 스냅샷/스테이트싱크 최소 구현
func (a *App) ListSnapshots(ctx context.Context, req *abci.ListSnapshotsRequest) (*abci.ListSnapshotsResponse, error) {
	return &abci.ListSnapshotsResponse{}, nil
}
func (a *App) OfferSnapshot(ctx context.Context, req *abci.OfferSnapshotRequest) (*abci.OfferSnapshotResponse, error) {
	return &abci.OfferSnapshotResponse{Result: abci.OFFER_SNAPSHOT_RESULT_REJECT}, nil
}
func (a *App) LoadSnapshotChunk(ctx context.Context, req *abci.LoadSnapshotChunkRequest) (*abci.LoadSnapshotChunkResponse, error) {
	return &abci.LoadSnapshotChunkResponse{}, nil
}
func (a *App) ApplySnapshotChunk(ctx context.Context, req *abci.ApplySnapshotChunkRequest) (*abci.ApplySnapshotChunkResponse, error) {
	return &abci.ApplySnapshotChunkResponse{Result: abci.APPLY_SNAPSHOT_CHUNK_RESULT_ACCEPT}, nil
}

func (a *App) Echo(ctx context.Context, req *abci.EchoRequest) (*abci.EchoResponse, error) {
	return &abci.EchoResponse{Message: req.Message}, nil
}

// -----------------------------
// Utilities
// -----------------------------

type Tx struct {
	Raw []byte
	NS  string
	Op  string
	K   string
	V   []byte
	Idx int // ← FinalizeBlock에서 결과를 제 위치에 넣기 위해 필요
}

func parseTx(raw []byte) (Tx, error) {
	// 포맷: put|<ns>|<key>|<value>
	parts := bytes.SplitN(raw, []byte("|"), 4)
	if len(parts) != 4 {
		return Tx{}, errors.New("bad tx: need 4 parts")
	}
	if string(parts[0]) != "put" {
		return Tx{}, fmt.Errorf("unsupported op: %s", parts[0])
	}
	return Tx{
		Raw: raw,
		Op:  "put",
		NS:  string(parts[1]),
		K:   string(parts[2]),
		V:   parts[3],
	}, nil
}

func groupByNS(txs []Tx) map[string][]Tx {
	m := make(map[string][]Tx, len(txs))
	for _, t := range txs {
		m[t.NS] = append(m[t.NS], t)
	}
	return m
}

func sequentialApply(mu *sync.RWMutex, st map[string][]byte, txs []Tx) {
	mu.Lock()
	defer mu.Unlock()
	for _, t := range txs {
		if t.Op == "put" {
			st[t.NS+"|"+t.K] = t.V
		}
	}
}

func computeAppHash(st map[string][]byte) []byte {
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		v := st[k]
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(v)))
		h.Write(l[:])
		h.Write(v)
	}
	return h.Sum(nil)
}
