// Package httpx hosts the HTTP adapter constructors for the agentflow root
// facade (checkpoint, retention, studio, webhook/human-gate, async jobs,
// production composition, and the observability dashboard) plus the scenario
// wiring helpers whose signatures reference root-facade types such as
// agentflow.Framework and agentflow.Option. Everything here may import the
// root package; constructors that do not need it live in pkg/adapters.
package httpx
