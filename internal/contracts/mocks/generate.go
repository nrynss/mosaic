// Package mocks contains generated GoMock implementations of Mosaic contracts.
package mocks

// GoMockVersion identifies the generator pinned in tools.go and go.mod.
const GoMockVersion = "v0.6.0"

//go:generate go tool mockgen -version
//go:generate go tool mockgen -source=../contracts.go -destination=contracts_mock.go -package=mocks
