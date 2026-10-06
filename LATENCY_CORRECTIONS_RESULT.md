# Correções após revisão de latência

Data: 2026-09-29. Working tree compartilhado; sem commit, migração de dados
reais, instaladores, restart da engine ou execução de modelos. Referências:
`LATENCY_INVESTIGATION.md`, `LATENCY_IMPLEMENTATION_PLAN.md`,
`LATENCY_IMPLEMENTATION_RESULT.md` e `LATENCY_REVIEW.md`.

## Resultado por achado

| Achado | Status | Contrato e arquivos |
| --- | --- | --- |
| R1 — Restore draft | **Resolvido (check focado)** | `clients/desktop/src/features/chat/MessageComposer.tsx` libera a guarda de gesto quando o editor deixa de exibir o draft submetido. Restaurar deliberadamente o mesmo objeto permite novo envio, inclusive com menções; cliques repetidos depois de limpar não criam outro envio. Teste preservado e ampliado em `MessageComposer.review.test.tsx`. O App continua verificando se existe draft mais novo antes de restaurar; fontes do side chat seguem com a submissão recuperável. |
| R2 — cursor SSE | **Resolvido no cliente (check focado)** | `App.tsx` e `features/chat/updatesConnection.ts`: erro fecha o EventSource e inicia timer de backoff (1–30 s); cada tentativa cria URL nova com `history.revision` atual de cada sessão inscrita. Cleanup de troca de inscrição/retry fecha conexão e cancela timer, mantendo uma conexão ativa. `updatesConnection.test.ts` cobre hidratação posterior à primeira abertura, revisão atual no retry, troca e cleanup; `sessionState.test.ts` cobre páginas mantidas num delta retomado. Reset genuíno por expiração continua descartando páginas. Rede real/WebView ainda pendente. |
| R3 — erro no snapshot final OpenCode | **Resolvido (check focado)** | `engine/harness.go` usa `withFinalBatch` para aplicar partes válidas e diagnóstico mesmo se a reconciliação sair cedo; `engine/opencode_interactive.go` isola detecção de `info.error`, interrompe a reconciliação e propaga falha de flush/validação. O batch é limpo por ciclo, antes do flush; nenhuma parte posterior inválida é publicada. `opencode_final_batch_test.go` verifica erro nativo no snapshot final, replay do journal e falha de persistência sem publicar partes. |
| R4 — commit genérico/checkpoint | **Parcial; objetivo global ainda incompleto** | `engine/models.go` e `engine/permission_policy.go` agora respondem fora de `app.mu` em configuração de modelo e YOLO, inclusive nas falhas. `engine/latency_investigation_bench_test.go` acrescenta benchmark de handlers reais YOLO/Stop em dados temporários. **`engine/store.go:commitLocked` e `engine/stream_journal.go:checkpointStreamJournal` permanecem globais e sob o mutex**, portanto envio/fila, steering, Stop, consulta/grafo e conclusão ainda podem depender do histórico alheio. Não houve alteração no formato/ordenação do WAL, aceitação durável ou replay; uma conversão parcial sem migração coerente poderia corromper revisões ou confirmar mensagens antes da durabilidade. R4 não está resolvido. |

## Checks e medidas

Comandos executados (somente recortes focados; primeira tentativa de Go/Vitest
foi interrompida pelo executor e não contou como sucesso):

```powershell
# engine
gofmt -w harness.go opencode_interactive.go opencode_final_batch_test.go models.go permission_policy.go latency_investigation_bench_test.go
go test -run '^TestOpenCodeFinalSnapshotErrorPersistsValidParts$' -count=1
go test -run '^Test(StreamJournalRecoveryAndNoOp|NewSubscriptionResetsAtCurrentRevision|StreamOwnsNativePayloadAndUsage|MessageIdentityReplay|MultiplexedStreamResumesObservedSession|OpenCodeFinalSnapshotErrorPersistsValidParts)$' -bench '^BenchmarkLatency(Investigation|Commands)$' -benchtime=3x -count=1 -benchmem
go test -run '^$' -bench '^BenchmarkLatencyCommands/send_' -benchtime=3x -count=1 -benchmem

# clients/desktop
npx tsc -b --pretty false
npx vitest run src/features/chat/sessionState.test.ts src/features/chat/SessionOptions.test.tsx src/features/chat/MessageComposer.review.test.tsx src/features/chat/updatesConnection.test.ts

# raiz
git diff --check
```

Go passou: 6 testes focados; benchmark em diretórios temporários, Windows amd64,
Ryzen 7 8700G. Typecheck passou; 14 testes Vitest em 4 arquivos passaram;
`git diff --check` sem erro de whitespace (avisos LF/CRLF do Git). Um typecheck
intermediário detectou erro de tipo no fixture novo e passou depois de corrigido.
Não foi executada suíte completa.

| Cenário | Tempo/operação, 3 iterações diagnósticas |
| --- | ---: |
| Stream real, 0 / 8 / 32 MiB alheios | 2,43 / 2,35 / 2,46 ms |
| Stream real, sessão ativa longa | 1,93 ms |
| 32 partes idênticas / 32 alteradas | 0,113 ms (0 commits) / 2,15 ms (1 commit) |
| Commit genérico, 0 / 8 / 32 MiB alheios | 4,31 / 106,14 / 305,45 ms |
| Handler real YOLO, 0 / 8 MiB alheios | 3,80 / 71,08 ms |
| Handler real Stop sem runtime, 0 / 8 MiB alheios | 7,97 / 150,44 ms |
| POST real de mensagem, turno ativo, modo queue, 0 / 8 MiB | 4,98 / 115,37 ms |
| POST real de mensagem, turno ativo, modo steer, 0 / 8 MiB | 5,40 / 109,97 ms |

POST mede preparo e aceite durável, sem executar harness nem claim/ack de
steering (turno ativo fictício somente para impedir despacho). Stop não mede
término de processo nativo. São amostras sintéticas,
não p95 nem comparação confiável de poucos milissegundos com execuções anteriores;
demonstram custo residual proporcional ao histórico não relacionado. Claim/ack
steering, configuração de catálogo de modelo e conclusão nativa **não foram medidos
como caminhos reais** nesta sessão; estas etapas requerem fixture adequada
sem iniciar modelo. Nenhuma migração de dados reais foi ensaiada.

## Pendências e validação humana

- R4 requer transações incrementais serializadas para comandos, consultas e grafos,
  snapshot/checkpoint fora da seção crítica com ordenação de revisões e replay de
  queda em cada fronteira. Migrar todos os leitores/escritores relevantes juntos,
  testar interleaving stream/comando/checkpoint e medir envio, steering,
  configuração e transições terminais reais. A otimização existente de streaming
  permanece válida mas não encerra este objetivo.
- Baseline OpenCode ainda percorre o histórico nativo em todo turno para uso e
  continuidade (`opencode_interactive.go:631`). `walkMessages` termina cedo somente
  quando recebe um baseline confiável, inexistente antes do novo turno; cache sem
  detecção de edições externas/restart perderia mensagens/uso. Custo de um turno
  longo não foi medido; requer watermark confirmado pelo harness ou benchmark
  isolado do transporte/paginação, sem heurística baseada em silêncio.
- Fluxo completo de Stop com POST pendente e entrega HTTP/SSE fora de ordem foi
  inspecionado em `App.tsx` (`stopVersions`, abort controllers, merge por revisão),
  mas ainda **não foi testado integrado**. Browser/WebView2/Focus/proxy, páginas
  antigas após queda de rede real, Pi/Codex/OpenCode reais, Playwright, perfis de
  render e retenção de runtime continuam validações humanas. Não mudar Choice/MCP,
  entrega incerta sem replay automático nem restrições de modelos durante turno.
