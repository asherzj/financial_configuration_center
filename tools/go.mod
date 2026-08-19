module github.com/asherzj/financial_configuration_center/tools

go 1.26.0

toolchain go1.26.6

// Kitex v0.16.2 still requests the pre-split genproto parent module. Force its
// post-split version so rpc/status has one provider: googleapis/rpc.
replace google.golang.org/genproto => google.golang.org/genproto v0.0.0-20260720211330-0afa2a65878a

tool (
	github.com/cloudwego/kitex/tool/cmd/kitex
	google.golang.org/grpc/cmd/protoc-gen-go-grpc
	google.golang.org/protobuf/cmd/protoc-gen-go
)

require (
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/cloudwego/gopkg v0.2.0 // indirect
	github.com/cloudwego/kitex v0.16.2 // indirect
	github.com/cloudwego/prutal v0.1.3 // indirect
	github.com/cloudwego/thriftgo v0.4.5 // indirect
	github.com/dlclark/regexp2 v1.11.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	golang.org/x/tools v0.41.0 // indirect
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.2 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
