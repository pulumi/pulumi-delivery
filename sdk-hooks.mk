# gen-sdk writes sdk/go/go.mod; the Go SDK module is sdk/go.mod (module .../sdk), so drop the nested one.
POST_GEN_SDK_GO = rm -f sdk/go/go.mod
