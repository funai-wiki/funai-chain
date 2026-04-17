package types

import (
	"context"

	"github.com/cosmos/cosmos-sdk/types/module"

	"google.golang.org/grpc"
)

func RegisterMsgServer(cfg module.Configurator, srv MsgServer) {
	cfg.MsgServer().RegisterService(&_Msg_serviceDesc, srv)
}

func RegisterQueryServer(cfg module.Configurator, srv QueryServer) {
	cfg.QueryServer().RegisterService(&_Query_serviceDesc, srv)
}

var _Msg_serviceDesc = grpc.ServiceDesc{
	ServiceName: "funai.settlement.Msg",
	HandlerType: (*MsgServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Deposit", Handler: _Msg_Deposit_Handler},
		{MethodName: "Withdraw", Handler: _Msg_Withdraw_Handler},
		{MethodName: "BatchSettle", Handler: _Msg_BatchSettle_Handler},
		{MethodName: "SubmitFraudProof", Handler: _Msg_SubmitFraudProof_Handler},
		{MethodName: "SubmitSecondVerificationResult", Handler: _Msg_SubmitSecondVerificationResult_Handler},
	},
	Streams: []grpc.StreamDesc{},
}

var _Query_serviceDesc = grpc.ServiceDesc{
	ServiceName: "funai.settlement.Query",
	HandlerType: (*QueryServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "InferenceAccount", Handler: _Query_InferenceAccount_Handler},
		{MethodName: "Batch", Handler: _Query_Batch_Handler},
		{MethodName: "Params", Handler: _Query_Params_Handler},
	},
	Streams: []grpc.StreamDesc{},
}

// -------- Msg Handlers --------

func _Msg_Deposit_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MsgDeposit)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MsgServer).Deposit(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/funai.settlement.Msg/Deposit"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MsgServer).Deposit(ctx, req.(*MsgDeposit))
	}
	return interceptor(ctx, in, info, handler)
}

func _Msg_Withdraw_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MsgWithdraw)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MsgServer).Withdraw(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/funai.settlement.Msg/Withdraw"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MsgServer).Withdraw(ctx, req.(*MsgWithdraw))
	}
	return interceptor(ctx, in, info, handler)
}

func _Msg_BatchSettle_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MsgBatchSettlement)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MsgServer).BatchSettle(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/funai.settlement.Msg/BatchSettle"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MsgServer).BatchSettle(ctx, req.(*MsgBatchSettlement))
	}
	return interceptor(ctx, in, info, handler)
}

func _Msg_SubmitFraudProof_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MsgFraudProof)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MsgServer).SubmitFraudProof(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/funai.settlement.Msg/SubmitFraudProof"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MsgServer).SubmitFraudProof(ctx, req.(*MsgFraudProof))
	}
	return interceptor(ctx, in, info, handler)
}

func _Msg_SubmitSecondVerificationResult_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MsgSecondVerificationResult)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(MsgServer).SubmitSecondVerificationResult(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/funai.settlement.Msg/SubmitSecondVerificationResult"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(MsgServer).SubmitSecondVerificationResult(ctx, req.(*MsgSecondVerificationResult))
	}
	return interceptor(ctx, in, info, handler)
}

// -------- Query Handlers --------

func _Query_InferenceAccount_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(QueryInferenceAccountRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(QueryServer).InferenceAccount(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/funai.settlement.Query/InferenceAccount"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(QueryServer).InferenceAccount(ctx, req.(*QueryInferenceAccountRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Query_Batch_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(QueryBatchRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(QueryServer).Batch(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/funai.settlement.Query/Batch"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(QueryServer).Batch(ctx, req.(*QueryBatchRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Query_Params_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(QueryParamsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(QueryServer).Params(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/funai.settlement.Query/Params"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(QueryServer).Params(ctx, req.(*QueryParamsRequest))
	}
	return interceptor(ctx, in, info, handler)
}
