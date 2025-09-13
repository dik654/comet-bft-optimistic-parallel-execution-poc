package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"
)

type rpcReq struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResp struct {
	Result interface{} `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func main() {
	endpoint := flag.String("rpc", "http://127.0.0.1:26657", "CometBFT RPC endpoint")
	total := flag.Int("n", 1000, "number of txs")
	conc := flag.Int("c", 32, "concurrency")
	ns := flag.String("ns", "bank", "namespace")
	flag.Parse()

	log.Printf("sending %d txs (c=%d) to %s", *total, *conc, *endpoint)

	work := make(chan []byte, *total)
	go func() {
		for i := 0; i < *total; i++ {
			k := fmt.Sprintf("k%06d", i)
			v := []byte(fmt.Sprintf("v%08d", i))
			raw := []byte(fmt.Sprintf("put|%s|%s|%s", *ns, k, v))
			work <- raw
		}
		close(work)
	}()

	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()

	errCh := make(chan error, *total)
	sem := make(chan struct{}, *conc)

	for raw := range work {
		sem <- struct{}{}
		go func(raw []byte) {
			defer func() { <-sem }()
			// base64 인코딩하여 broadcast_tx_async
			b64 := base64.StdEncoding.EncodeToString(raw)
			payload := rpcReq{JSONRPC: "2.0", ID: rand.Int(), Method: "broadcast_tx_async", Params: []interface{}{b64}}
			body, _ := json.Marshal(payload)
			resp, err := client.Post(*endpoint, "application/json", bytes.NewReader(body))
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()
			var out rpcResp
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				errCh <- err
				return
			}
			if out.Error != nil {
				errCh <- fmt.Errorf("rpc error %d: %s", out.Error.Code, out.Error.Message)
				return
			}
			errCh <- nil
		}(raw)
	}

	// drain
	for i := 0; i < *conc; i++ {
		sem <- struct{}{}
	}

	var fail int
	for i := 0; i < *total; i++ {
		if err := <-errCh; err != nil {
			fail++
		}
	}

	dur := time.Since(start)
	log.Printf("done: sent=%d fail=%d dur=%s tps=%.1f", *total, fail, dur, float64(*total-fail)/dur.Seconds())
}
