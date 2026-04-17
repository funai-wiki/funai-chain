package app

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/cosmos/cosmos-sdk/server/api"
	sdk "github.com/cosmos/cosmos-sdk/types"

	settlementkeeper "github.com/funai-wiki/funai-chain/x/settlement/keeper"
	settlementtypes "github.com/funai-wiki/funai-chain/x/settlement/types"
	workerkeeper "github.com/funai-wiki/funai-chain/x/worker/keeper"
	workertypes "github.com/funai-wiki/funai-chain/x/worker/types"
)

// RegisterCustomRESTRoutes registers REST API endpoints for FunAI custom modules.
// These modules use synthetic proto descriptors, so standard grpc-gateway registration
// doesn't work. Instead, we register HTTP handlers that query the latest committed state.
func RegisterCustomRESTRoutes(apiSvr *api.Server, app *FunAIApp) {
	r := apiSvr.Router

	// Helper: create SDK context from latest committed state
	queryCtx := func() sdk.Context {
		ctx, _ := app.CreateQueryContext(0, false)
		return ctx
	}

	registerSettlementRoutes(r, app.SettlementKeeper, queryCtx)
	registerWorkerRoutes(r, app.WorkerKeeper, queryCtx)
}

func registerSettlementRoutes(r *mux.Router, k settlementkeeper.Keeper, queryCtx func() sdk.Context) {
	r.HandleFunc("/funai/settlement/v1/account/{address}", func(w http.ResponseWriter, r *http.Request) {
		addr := mux.Vars(r)["address"]
		accAddr, err := sdk.AccAddressFromBech32(addr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid address: "+err.Error())
			return
		}
		ia, found := k.GetInferenceAccount(queryCtx(), accAddr)
		if !found {
			writeError(w, http.StatusNotFound, "inference account not found: "+addr)
			return
		}
		writeJSON(w, settlementtypes.QueryInferenceAccountResponse{Account: ia})
	}).Methods("GET")

	r.HandleFunc("/funai/settlement/v1/batch/{batch_id}", func(w http.ResponseWriter, r *http.Request) {
		batchId, err := strconv.ParseUint(mux.Vars(r)["batch_id"], 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid batch_id")
			return
		}
		batch, found := k.GetBatchRecord(queryCtx(), batchId)
		if !found {
			writeError(w, http.StatusNotFound, "batch not found")
			return
		}
		writeJSON(w, settlementtypes.QueryBatchResponse{Batch: batch})
	}).Methods("GET")

	r.HandleFunc("/funai/settlement/v1/params", func(w http.ResponseWriter, _ *http.Request) {
		params := k.GetParams(queryCtx())
		writeJSON(w, settlementtypes.QueryParamsResponse{Params: params})
	}).Methods("GET")
}

func registerWorkerRoutes(r *mux.Router, k workerkeeper.Keeper, queryCtx func() sdk.Context) {
	r.HandleFunc("/funai/worker/v1/workers", func(w http.ResponseWriter, _ *http.Request) {
		workers := k.GetAllWorkers(queryCtx())
		writeJSON(w, workertypes.QueryWorkersResponse{Workers: workers})
	}).Methods("GET")

	r.HandleFunc("/funai/worker/v1/worker/{address}", func(w http.ResponseWriter, r *http.Request) {
		addr := mux.Vars(r)["address"]
		accAddr, err := sdk.AccAddressFromBech32(addr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid address: "+err.Error())
			return
		}
		worker, found := k.GetWorker(queryCtx(), accAddr)
		if !found {
			writeError(w, http.StatusNotFound, "worker not found: "+addr)
			return
		}
		writeJSON(w, workertypes.QueryWorkerResponse{Worker: worker})
	}).Methods("GET")

	r.HandleFunc("/funai/worker/v1/params", func(w http.ResponseWriter, _ *http.Request) {
		params := k.GetParams(queryCtx())
		writeJSON(w, workertypes.QueryParamsResponse{Params: params})
	}).Methods("GET")
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
