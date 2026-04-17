package types

import "context"

// QueryServer defines the modelreg module's gRPC query service.
type QueryServer interface {
	Model(context.Context, *QueryModelRequest) (*QueryModelResponse, error)
	ModelByAlias(context.Context, *QueryModelByAliasRequest) (*QueryModelResponse, error)
	Models(context.Context, *QueryModelsRequest) (*QueryModelsResponse, error)
	Params(context.Context, *QueryParamsRequest) (*QueryParamsResponse, error)
}

// -------- Model --------

type QueryModelRequest struct {
	ModelId string `protobuf:"bytes,1,opt,name=model_id,proto3" json:"model_id"`
}

func (m *QueryModelRequest) ProtoMessage()  {}
func (m *QueryModelRequest) Reset()         { *m = QueryModelRequest{} }
func (m *QueryModelRequest) String() string { return "QueryModelRequest" }

type QueryModelResponse struct {
	Model Model `protobuf:"bytes,1,opt,name=model,proto3" json:"model"`
}

func (m *QueryModelResponse) ProtoMessage()  {}
func (m *QueryModelResponse) Reset()         { *m = QueryModelResponse{} }
func (m *QueryModelResponse) String() string { return "QueryModelResponse" }

// -------- ModelByAlias --------

type QueryModelByAliasRequest struct {
	Alias string `protobuf:"bytes,1,opt,name=alias,proto3" json:"alias"`
}

func (m *QueryModelByAliasRequest) ProtoMessage()  {}
func (m *QueryModelByAliasRequest) Reset()         { *m = QueryModelByAliasRequest{} }
func (m *QueryModelByAliasRequest) String() string { return "QueryModelByAliasRequest" }

// -------- Models --------

type QueryModelsRequest struct{}

func (m *QueryModelsRequest) ProtoMessage()  {}
func (m *QueryModelsRequest) Reset()         { *m = QueryModelsRequest{} }
func (m *QueryModelsRequest) String() string { return "QueryModelsRequest" }

type QueryModelsResponse struct {
	Models []Model `protobuf:"bytes,1,rep,name=models,proto3" json:"models"`
}

func (m *QueryModelsResponse) ProtoMessage()  {}
func (m *QueryModelsResponse) Reset()         { *m = QueryModelsResponse{} }
func (m *QueryModelsResponse) String() string { return "QueryModelsResponse" }

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
