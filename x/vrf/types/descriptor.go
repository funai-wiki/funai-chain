package types

import (
	"github.com/funai-wiki/funai-chain/pkg/protodesc"
)

var vrfDescGz []byte

func init() {
	vrfDescGz = protodesc.BuildAndRegister(protodesc.FileDescriptor{
		FileName:    "funai/vrf/types.proto",
		PackageName: "funai.vrf",
		Messages: []protodesc.MsgEntry{
			{Name: "MsgSubmitVRFProof", Instance: MsgSubmitVRFProof{}},
			{Name: "MsgSubmitVRFProofResponse", Instance: MsgSubmitVRFProofResponse{}},
			{Name: "MsgLeaderHeartbeat", Instance: MsgLeaderHeartbeat{}},
			{Name: "MsgLeaderHeartbeatResponse", Instance: MsgLeaderHeartbeatResponse{}},
			{Name: "MsgReportLeaderTimeout", Instance: MsgReportLeaderTimeout{}},
			{Name: "MsgReportLeaderTimeoutResponse", Instance: MsgReportLeaderTimeoutResponse{}},
			{Name: "LeaderInfo", Instance: LeaderInfo{}},
			{Name: "CommitteeMember", Instance: CommitteeMember{}},
			{Name: "CommitteeInfo", Instance: CommitteeInfo{}},
			{Name: "VRFSeed", Instance: VRFSeed{}},
			{Name: "QueryParamsRequest", Instance: QueryParamsRequest{}},
			{Name: "QueryParamsResponse", Instance: QueryParamsResponse{}},
			{Name: "QueryCurrentSeedRequest", Instance: QueryCurrentSeedRequest{}},
			{Name: "QueryCurrentSeedResponse", Instance: QueryCurrentSeedResponse{}},
			{Name: "QueryLeaderRequest", Instance: QueryLeaderRequest{}},
			{Name: "QueryLeaderResponse", Instance: QueryLeaderResponse{}},
			{Name: "QueryCommitteeRequest", Instance: QueryCommitteeRequest{}},
			{Name: "QueryCommitteeResponse", Instance: QueryCommitteeResponse{}},
		},
		Services: []protodesc.ServiceEntry{
			{
				Name: "Msg",
				Methods: []protodesc.MethodEntry{
					{Name: "SubmitVRFProof", InputType: ".funai.vrf.MsgSubmitVRFProof", OutputType: ".funai.vrf.MsgSubmitVRFProofResponse"},
					{Name: "LeaderHeartbeat", InputType: ".funai.vrf.MsgLeaderHeartbeat", OutputType: ".funai.vrf.MsgLeaderHeartbeatResponse"},
					{Name: "ReportLeaderTimeout", InputType: ".funai.vrf.MsgReportLeaderTimeout", OutputType: ".funai.vrf.MsgReportLeaderTimeoutResponse"},
				},
			},
			{
				Name: "Query",
				Methods: []protodesc.MethodEntry{
					{Name: "Params", InputType: ".funai.vrf.QueryParamsRequest", OutputType: ".funai.vrf.QueryParamsResponse"},
					{Name: "CurrentSeed", InputType: ".funai.vrf.QueryCurrentSeedRequest", OutputType: ".funai.vrf.QueryCurrentSeedResponse"},
					{Name: "Leader", InputType: ".funai.vrf.QueryLeaderRequest", OutputType: ".funai.vrf.QueryLeaderResponse"},
					{Name: "Committee", InputType: ".funai.vrf.QueryCommitteeRequest", OutputType: ".funai.vrf.QueryCommitteeResponse"},
				},
			},
		},
	})
}

func (m *MsgSubmitVRFProof) Descriptor() ([]byte, []int)              { return vrfDescGz, []int{0} }
func (m *MsgSubmitVRFProofResponse) Descriptor() ([]byte, []int)      { return vrfDescGz, []int{1} }
func (m *MsgLeaderHeartbeat) Descriptor() ([]byte, []int)             { return vrfDescGz, []int{2} }
func (m *MsgLeaderHeartbeatResponse) Descriptor() ([]byte, []int)     { return vrfDescGz, []int{3} }
func (m *MsgReportLeaderTimeout) Descriptor() ([]byte, []int)         { return vrfDescGz, []int{4} }
func (m *MsgReportLeaderTimeoutResponse) Descriptor() ([]byte, []int) { return vrfDescGz, []int{5} }
func (m *LeaderInfo) Descriptor() ([]byte, []int)                     { return vrfDescGz, []int{6} }
func (m *CommitteeMember) Descriptor() ([]byte, []int)                { return vrfDescGz, []int{7} }
func (m *CommitteeInfo) Descriptor() ([]byte, []int)                  { return vrfDescGz, []int{8} }
func (m *VRFSeed) Descriptor() ([]byte, []int)                        { return vrfDescGz, []int{9} }
func (m *QueryParamsRequest) Descriptor() ([]byte, []int)             { return vrfDescGz, []int{10} }
func (m *QueryParamsResponse) Descriptor() ([]byte, []int)            { return vrfDescGz, []int{11} }
func (m *QueryCurrentSeedRequest) Descriptor() ([]byte, []int)        { return vrfDescGz, []int{12} }
func (m *QueryCurrentSeedResponse) Descriptor() ([]byte, []int)       { return vrfDescGz, []int{13} }
func (m *QueryLeaderRequest) Descriptor() ([]byte, []int)             { return vrfDescGz, []int{14} }
func (m *QueryLeaderResponse) Descriptor() ([]byte, []int)            { return vrfDescGz, []int{15} }
func (m *QueryCommitteeRequest) Descriptor() ([]byte, []int)          { return vrfDescGz, []int{16} }
func (m *QueryCommitteeResponse) Descriptor() ([]byte, []int)         { return vrfDescGz, []int{17} }
