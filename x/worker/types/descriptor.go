package types

import (
	"github.com/funai-wiki/funai-chain/pkg/protodesc"
)

var workerDescGz []byte

func init() {
	workerDescGz = protodesc.BuildAndRegister(protodesc.FileDescriptor{
		FileName:    "funai/worker/types.proto",
		PackageName: "funai.worker",
		Messages: []protodesc.MsgEntry{
			{Name: "MsgRegisterWorker", Instance: MsgRegisterWorker{}},
			{Name: "MsgExitWorker", Instance: MsgExitWorker{}},
			{Name: "MsgUpdateModels", Instance: MsgUpdateModels{}},
			{Name: "MsgStake", Instance: MsgStake{}},
			{Name: "MsgUnjail", Instance: MsgUnjail{}},
			{Name: "MsgRegisterWorkerResponse", Instance: MsgRegisterWorkerResponse{}},
			{Name: "MsgExitWorkerResponse", Instance: MsgExitWorkerResponse{}},
			{Name: "MsgUpdateModelsResponse", Instance: MsgUpdateModelsResponse{}},
			{Name: "MsgStakeResponse", Instance: MsgStakeResponse{}},
			{Name: "MsgUnjailResponse", Instance: MsgUnjailResponse{}},
			{Name: "Worker", Instance: Worker{}},
			{Name: "QueryWorkerRequest", Instance: QueryWorkerRequest{}},
			{Name: "QueryWorkerResponse", Instance: QueryWorkerResponse{}},
			{Name: "QueryWorkersRequest", Instance: QueryWorkersRequest{}},
			{Name: "QueryWorkersResponse", Instance: QueryWorkersResponse{}},
			{Name: "QueryWorkersByModelRequest", Instance: QueryWorkersByModelRequest{}},
			{Name: "QueryWorkersByModelResponse", Instance: QueryWorkersByModelResponse{}},
			{Name: "QueryParamsRequest", Instance: QueryParamsRequest{}},
			{Name: "QueryParamsResponse", Instance: QueryParamsResponse{}},
		},
		Services: []protodesc.ServiceEntry{
			{
				Name: "Msg",
				Methods: []protodesc.MethodEntry{
					{Name: "RegisterWorker", InputType: ".funai.worker.MsgRegisterWorker", OutputType: ".funai.worker.MsgRegisterWorkerResponse"},
					{Name: "ExitWorker", InputType: ".funai.worker.MsgExitWorker", OutputType: ".funai.worker.MsgExitWorkerResponse"},
					{Name: "UpdateModels", InputType: ".funai.worker.MsgUpdateModels", OutputType: ".funai.worker.MsgUpdateModelsResponse"},
					{Name: "AddStake", InputType: ".funai.worker.MsgStake", OutputType: ".funai.worker.MsgStakeResponse"},
					{Name: "Unjail", InputType: ".funai.worker.MsgUnjail", OutputType: ".funai.worker.MsgUnjailResponse"},
				},
			},
			{
				Name: "Query",
				Methods: []protodesc.MethodEntry{
					{Name: "Worker", InputType: ".funai.worker.QueryWorkerRequest", OutputType: ".funai.worker.QueryWorkerResponse"},
					{Name: "Workers", InputType: ".funai.worker.QueryWorkersRequest", OutputType: ".funai.worker.QueryWorkersResponse"},
					{Name: "WorkersByModel", InputType: ".funai.worker.QueryWorkersByModelRequest", OutputType: ".funai.worker.QueryWorkersByModelResponse"},
					{Name: "Params", InputType: ".funai.worker.QueryParamsRequest", OutputType: ".funai.worker.QueryParamsResponse"},
				},
			},
		},
	})
}

func (m *MsgRegisterWorker) Descriptor() ([]byte, []int)           { return workerDescGz, []int{0} }
func (m *MsgExitWorker) Descriptor() ([]byte, []int)               { return workerDescGz, []int{1} }
func (m *MsgUpdateModels) Descriptor() ([]byte, []int)             { return workerDescGz, []int{2} }
func (m *MsgStake) Descriptor() ([]byte, []int)                    { return workerDescGz, []int{3} }
func (m *MsgUnjail) Descriptor() ([]byte, []int)                   { return workerDescGz, []int{4} }
func (m *MsgRegisterWorkerResponse) Descriptor() ([]byte, []int)   { return workerDescGz, []int{5} }
func (m *MsgExitWorkerResponse) Descriptor() ([]byte, []int)       { return workerDescGz, []int{6} }
func (m *MsgUpdateModelsResponse) Descriptor() ([]byte, []int)     { return workerDescGz, []int{7} }
func (m *MsgStakeResponse) Descriptor() ([]byte, []int)            { return workerDescGz, []int{8} }
func (m *MsgUnjailResponse) Descriptor() ([]byte, []int)           { return workerDescGz, []int{9} }
func (m *Worker) Descriptor() ([]byte, []int)                      { return workerDescGz, []int{10} }
func (m *QueryWorkerRequest) Descriptor() ([]byte, []int)          { return workerDescGz, []int{11} }
func (m *QueryWorkerResponse) Descriptor() ([]byte, []int)         { return workerDescGz, []int{12} }
func (m *QueryWorkersRequest) Descriptor() ([]byte, []int)         { return workerDescGz, []int{13} }
func (m *QueryWorkersResponse) Descriptor() ([]byte, []int)        { return workerDescGz, []int{14} }
func (m *QueryWorkersByModelRequest) Descriptor() ([]byte, []int)  { return workerDescGz, []int{15} }
func (m *QueryWorkersByModelResponse) Descriptor() ([]byte, []int) { return workerDescGz, []int{16} }
func (m *QueryParamsRequest) Descriptor() ([]byte, []int)          { return workerDescGz, []int{17} }
func (m *QueryParamsResponse) Descriptor() ([]byte, []int)         { return workerDescGz, []int{18} }
