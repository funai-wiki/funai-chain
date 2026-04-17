package types

import "context"

// QueryServer defines the worker module's gRPC query service.
type QueryServer interface {
	Worker(context.Context, *QueryWorkerRequest) (*QueryWorkerResponse, error)
	Workers(context.Context, *QueryWorkersRequest) (*QueryWorkersResponse, error)
	WorkersByModel(context.Context, *QueryWorkersByModelRequest) (*QueryWorkersByModelResponse, error)
	Params(context.Context, *QueryParamsRequest) (*QueryParamsResponse, error)
}

// -------- Worker --------

type QueryWorkerRequest struct {
	Address string `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
}

func (m *QueryWorkerRequest) ProtoMessage()  {}
func (m *QueryWorkerRequest) Reset()         { *m = QueryWorkerRequest{} }
func (m *QueryWorkerRequest) String() string { return "QueryWorkerRequest" }

type QueryWorkerResponse struct {
	Worker Worker `protobuf:"bytes,1,opt,name=worker,proto3" json:"worker"`
}

func (m *QueryWorkerResponse) ProtoMessage()  {}
func (m *QueryWorkerResponse) Reset()         { *m = QueryWorkerResponse{} }
func (m *QueryWorkerResponse) String() string { return "QueryWorkerResponse" }

// -------- Workers --------

type QueryWorkersRequest struct{}

func (m *QueryWorkersRequest) ProtoMessage()  {}
func (m *QueryWorkersRequest) Reset()         { *m = QueryWorkersRequest{} }
func (m *QueryWorkersRequest) String() string { return "QueryWorkersRequest" }

type QueryWorkersResponse struct {
	Workers []Worker `protobuf:"bytes,1,rep,name=workers,proto3" json:"workers"`
}

func (m *QueryWorkersResponse) ProtoMessage()  {}
func (m *QueryWorkersResponse) Reset()         { *m = QueryWorkersResponse{} }
func (m *QueryWorkersResponse) String() string { return "QueryWorkersResponse" }

// -------- WorkersByModel --------

type QueryWorkersByModelRequest struct {
	ModelId string `protobuf:"bytes,1,opt,name=model_id,proto3" json:"model_id"`
}

func (m *QueryWorkersByModelRequest) ProtoMessage()  {}
func (m *QueryWorkersByModelRequest) Reset()         { *m = QueryWorkersByModelRequest{} }
func (m *QueryWorkersByModelRequest) String() string { return "QueryWorkersByModelRequest" }

type QueryWorkersByModelResponse struct {
	Workers []Worker `protobuf:"bytes,1,rep,name=workers,proto3" json:"workers"`
}

func (m *QueryWorkersByModelResponse) ProtoMessage()  {}
func (m *QueryWorkersByModelResponse) Reset()         { *m = QueryWorkersByModelResponse{} }
func (m *QueryWorkersByModelResponse) String() string { return "QueryWorkersByModelResponse" }

// -------- Params --------

type QueryParamsRequest struct{}

func (m *QueryParamsRequest) ProtoMessage()  {}
func (m *QueryParamsRequest) Reset()         { *m = QueryParamsRequest{} }
func (m *QueryParamsRequest) String() string { return "QueryParamsRequest" }

type QueryParamsResponse struct {
	Params Params `protobuf:"bytes,1,opt,name=params,proto3" json:"params"`
}

func (m *QueryParamsResponse) ProtoMessage()  {}
func (m *QueryParamsResponse) Reset()         { *m = QueryParamsResponse{} }
func (m *QueryParamsResponse) String() string { return "QueryParamsResponse" }
