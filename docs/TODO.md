# cli-wrapper TODO

> Post-v0.2.0 작업 리스트. 2026-04-10 기준.

## 🔴 High

- [x] CHANGELOG `[0.1.0]` Known Limitations에서 "`cliwrap events` 미구현" 불릿 제거 (v0.2.0에서 구현 완료)
- [x] v0.2.1 패치 태그 — gocritic lint fix + test budget 확대 반영

## 🟡 Medium

- [ ] `cliwrap logs --since <duration>` / `--lines N` 필터 — RingBuffer에 line boundary/timestamp 추적 추가 필요 (~1일)
- [ ] `bubblewrap` sandbox provider — `pkg/sandbox/providers/bubblewrap/` (~1-2일)
- [ ] Logs integration test Phase 2 flake 근본 원인 조사 — IPC latency spike (agent io.Copy flush? outbox backpressure? macOS net.Pipe?) (~2-4시간)

## 🟢 Low

- [ ] `cliwrap events --type` 필터 — `event.Filter.Types` 이미 존재, CLI flag + wire field 추가 (~2시간)
- [ ] 추가 sandbox providers — `firejail` (Linux), `sandbox-exec` (macOS) (각 ~1일)
- [ ] Benchmark 회귀 추적 — `benchstat` + CI 스크립트 (~반일)
- [ ] `.github/FUNDING.yml` — 스폰서십 설정 시

## 🗺️ Roadmap

- [ ] cgroups v2 throttling — CPU quota, I/O weight, Linux 전용 (~2-3일)
- [ ] Windows 지원 — Named pipes, Job Objects (~1-2주)
- [ ] File-backed log persistence — `rotator.go` 미사용 코드 연결 (~반일)
- [ ] Ring buffer 사이즈 설정 — 현재 1 MiB 하드코딩 → `ManagerOption` + YAML key (~2시간)
