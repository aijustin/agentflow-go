package mock

import pkgmock "github.com/aijustin/agentflow-go/pkg/llm/mock"

type Gateway = pkgmock.Gateway

var ErrNoResponse = pkgmock.ErrNoResponse

var NewGateway = pkgmock.NewGateway

type FallbackGateway = pkgmock.FallbackGateway

var NewFallbackGateway = pkgmock.NewFallbackGateway
