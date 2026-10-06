# Resultado da implementação de latência

Data: 2026-09-29. Implementação no working tree compartilhado, sem commit, sem
instalador e sem tocar na engine instalada nem em seus dados. As alterações já
existentes antes desta sessão (colaboração entre sessões, UI, documentação e
outros arquivos) foram preservadas. Referências: `LATENCY_INVESTIGATION.md` e
`LATENCY_IMPLEMENTATION_PLAN.md`.

## Checklist e arquivos alterados nesta sessão

1. **Transporte — implementado, validação humana pendente.** `engine/updates.go`,
   `engine/api.go`, `engine/runtime.go`, `engine/store.go`, `clients/desktop/src/App.tsx`,
   `clients/desktop/src/features/chat/sessionState.ts`. Uma conexão `/api/updates`
   por instância do frontend; inventário de sessões e projetos no início; resumos
   alterados durante a conexão; páginas/deltas somente das conversas inscritas.
   A sessão ativa, side chat visível e subagentes ativos/abertos são inscrições;
   `runtimeActive` não cria inscrição. A rota SSE antiga continua para clientes
   anteriores. O polling `/api/state` passa a ser fallback a cada 30 s; o polling
   periódico de grafo só atua com SSE desconectado. Inventário atualizado cobre
   criação/remoção/reordenação; a descoberta de sessões criadas substitui a
   varredura de `session_spawn` no histórico carregado.
2. **Persistência — hot path implementado; checkpoint genérico ainda custoso.**
   `engine/stream_journal.go`, `engine/stream.go`, `engine/store.go`, `engine/history.go`,
   `engine/main.go`, `engine/graph_records.go`, `engine/graph_snapshot.go`,
   `engine/usage.go`. Delta de streaming e telemetria de tokens são fsync de um
   registro pequeno, aplicado somente depois da durabilidade; upsert idêntico
   não grava nem avança revisão. Histórico não relacionado não é clonado,
   comparado ou serializado nesse hot path. Checkpoint genérico permanece para
   fila/consultas/grafos e consolida o journal. Checkpoint periódico a cada 5 min
   limita replay. A seção crítica global ainda inclui fsync do registro e, nos
   checkpoints, snapshot completo; índices de evento por sessão tornam busca de
   IDs proporcional ao lote, não ao histórico ativo. Ver riscos abaixo.
3. **Encerramento/steering — parcial.** `engine/opencode_interactive.go`,
   `engine/harness.go`, `engine/pi_interactive.go`, `engine/codex_interactive.go`.
   Reconciliação final OpenCode aplica partes em lote e elimina no-ops; Pi não
   espera até 2 s por telemetria após `agent_settled`; Codex despacha steering
   antes de drenar mais frames quando o canal já está pronto. IDs de estatísticas
   Pi incluem identidade do turno para que respostas tardias não contaminem outro
   turno. Nenhuma regra de
   terminalidade nativa, Choice/MCP, Playwright ou Stop de processos foi relaxada.
   Baseline OpenCode no início do turno ainda percorre o histórico.
4. **Catálogos — implementado, validação com harnesses pendente.**
   `engine/models.go`, `engine/main.go`,
   `clients/desktop/src/features/chat/modelCatalog.ts`, `ModelPicker.tsx`.
   Single-flight backend por projeto/harness, descoberta fora de `catalogMu`,
   contexto de 45 s, cancelamento quando não há consumidores, hits de outras
   chaves independentes. Cache de UI compartilhado por URL/projeto/harness,
   retenção stale durante refresh, cancelamento do último consumidor, deduplicação.
   Seleção continua validada na engine. Reconfiguração externa do harness depende
   de refresh explícito ou expiração do TTL de 2 min.
5. **Otimismo — implementado para ações principais, cobertura parcial.**
   `engine/api.go`, `engine/store.go`, `engine/message_identity.go`,
   `clients/desktop/src/App.tsx`, `engine.ts`, `MessageComposer.tsx`,
   `PendingSubmission.tsx`, `SideChatPanel.tsx`, `SessionOptions.tsx`,
   `ModelPicker.tsx`, `sessionState.ts`. Rascunho limpa antes do POST; editor aceita
   nova digitação. Card local mostra envio ou recuperação, inclusive fontes do
   side chat. Resposta HTTP aplica eventos carregados sem mover o cursor SSE;
   SSE reconcilia pelo mesmo ID. 4xx definitivo conserva rascunho recuperável;
   transporte/5xx é incerto, sem replay automático. Reenvio manual usa o mesmo ID
   idempotente; restauração não apaga um rascunho novo. Stop ignora o bloqueio de
   envio. YOLO, grafo, modelo e esforço têm preview local e rollback de falha.
   Intenções de modelo/grafo ficam no App e a de YOLO é compartilhada por
   engine/sessão, preservando exclusão e preview durante remount do controle.
   Modelo com sucesso conserva preview até o snapshot confirmado chegar. Envios
   locais são serializados por conversa sem travar o editor. O POST possui limite
   de 45 s; timeout mantém entrega incerta e nunca gera replay automático. Stop aborta requests
   em voo e invalida despachos locais anteriores; a engine revalida Stop depois
   de preparar anexos. Pendentes são recuperáveis por `sessionStorage`, incluindo
   texto, menções, fontes e o mesmo ID, sem reenvio automático ao recarregar.
6. **UI/recuperação — parcial.** `clients/desktop/src/features/chat/ChatMessage.tsx`,
   `ConversationEvents.tsx`, `sessionState.ts`,
   `clients/desktop/src/design-system/AgentWork.tsx`, `App.tsx`, `engine.ts`.
   Merges sem mudança mantêm identidade; Markdown é memoizado; grupos de trabalho
   antigos só renderizam novamente se seus eventos mudarem; detalhes pesados são
   montados ao abrir. Timeout, abort, HTTP 4xx e falha de SSE são distinguíveis;
   pacote saudável limpa alerta de stream. Falha isolada de polling não bloqueia
   composer/side chat. Não houve medição de render real que justificasse virtualização.

## Contratos de persistência e recuperação

- `state.json` v2 existente continua sendo o checkpoint completo e legível; novos
  campos opcionais `acceptedMessages` (chave `sessionId/clientId` → SHA-256 do
  texto, modo, fontes e menções) e `graphViewRevision` são lidos pela engine nova
  também quando ausentes em checkpoints anteriores. Binários antigos que usam
  `DisallowUnknownFields` recusam esses novos campos; downgrade do armazenamento
  não está implementado. Não foi feita migração com dados reais.
- `stream.journal` é sequência de frames `uint32 LE tamanho`, `uint32 LE CRC32`,
  JSON `{revision,session,updated,updates:[{event,appendText}],usage?}`. Registros de
  append contêm o **delta**, não o texto inteiro da sessão. `Sync` precede a
  publicação na memória/SSE. Falha de gravação torna a engine read-only e cancela
  turns, como falha do checkpoint. O replay parte de `state.json`, aplica apenas
  revisões posteriores, exige sequência contígua, trunca frame final incompleto
  e recusa frame completo inválido/CRC ruim ou sessão ausente. Payloads nativos
  são copiados antes de entrar no batch, para não compartilhar mapas mutáveis do
  adapter com o estado publicado. Exportação copia sua janela antes de soltar o
  mutex; o streaming não copia toda a sessão a cada delta.
- Um commit genérico ainda clona e valida o `diskState`, grava `state.json` por
  replace atômico e só então remove o journal. Queda entre replace e remoção é
  recuperada pelo filtro de revisões; a fila e os registros de grafo continuam
  juntos no mesmo checkpoint. Nenhuma rota legada grava um checkpoint mais antigo
  sem possuir `app.mu`; após replay, o estado inteiro consolidado é o ponto de
  partida para as rotas antigas. O checkpoint periódico segue o mesmo lock.
- O journal de deltas SSE em memória retém mudanças por conversa, com limite e
  reset paginado quando necessário. Uma revisão global de stream não altera a
  revisão da projeção de grafo. Reabertura do frontend não exige journal SSE em
  disco: o servidor emite reset inicial de histórico observado.

## Contratos de transporte e identidades

- `/api/updates?ids=id1,id2&cursors=<JSON por sessão>` devolve `kind:inventory`
  (resumos de sessões visíveis, projetos, harnesses), seguido por `kind:session`.
  Cada ID sem cursor recebe reset paginado; cursor válido retoma os deltas;
  cursor fora do journal retido exige reset. Listener é registrado antes do
  snapshot. Wake de stream marca somente a conversa alterada; commits genéricos
  ainda reavaliam todos os resumos. Conversas observadas recebem seu summary
  junto com o delta, sem pacote duplicado. Background recebe somente summary.
  Troca de inscrições fecha e reabre a **única** conexão com cursores individuais.
  Reconexão automática reaplica a mesma consulta de forma idempotente. Reset
  invalida páginas antigas potencialmente desatualizadas; retomada normal conserva
  a paginação carregada. Projetos ou ordem/conjunto de sessões alterados produzem
  novo inventário durante a conexão. Paginação anterior continua no endpoint próprio.
- O POST de mensagem aceita `clientId` opcional; cliente novo usa UUID gerado
  antes do request. A fila e o evento aceito usam esse ID. Um retry com mesmo ID
  e mesmo payload retorna o estado atual sem segunda aceitação; payload diferente
  recebe 409. IDs de clientes antigos continuam sendo gerados no servidor.
  Fingerprint e fila entram no mesmo checkpoint atômico. Resposta HTTP aplica
  alterações de eventos somente se os índices carregados permitirem, sem avançar
  `history.revision`; o stream permanece autoridade para preencher lacunas.
  Revisão por objeto de evento impede um delta SSE anterior de reverter conteúdo
  HTTP mais recente. Um ID já usado em outra conversa é recusado, porque os
  payloads de fila possuem um índice global.

## Verificação e medidas

- `go test -run '^$'` — compilação Go sem executar a suíte: passou.
- `npx tsc -b --pretty false` em `clients/desktop` — typecheck: passou.
- `gofmt -w` nos arquivos Go desta implementação e `git diff --check` — sem
  erros de formatação/whitespace; Git apenas informou a política de CRLF local.
- `go test -run '^Test(StreamJournalRecoveryAndNoOp|NewSubscriptionResetsAtCurrentRevision|StreamOwnsNativePayloadAndUsage|MessageIdentityReplay|MultiplexedStreamResumesObservedSession)$'`
  — 5 testes passaram: replay, frame parcial, checkpoint sobreposto, no-op,
  payload nativo imutável, uso de tokens, retry/conflito de ID e multiplexação
  com cursor válido para um chat e reset para outro na mesma conexão.
- `npx vitest run src/features/chat/sessionState.test.ts src/features/chat/SessionOptions.test.tsx`
  — os 8 testes de transporte passaram; os 2 testes novos de YOLO inicialmente
  encontraram falta de `ResizeObserver` no ambiente JSDOM. A fixture foi corrigida
  e `npx vitest run src/features/chat/SessionOptions.test.tsx` passou com ambos:
  preview/rollback (incluindo remount pendente) e restrição durante execução. Sem suíte completa.
- `go test -run '^$' -bench '^BenchmarkLatencyInvestigation$' -benchtime=3x -count=1 -benchmem`
  em `engine` — execução final passou; estado sintético em diretórios temporários,
  Ryzen 7 8700G / Windows amd64. Três iterações por cenário, média diagnóstica,
  não p95 de produção. Um ensaio intermediário do benchmark com novo caso falhou
  por fixture sem `turn`; fixture corrigida e comando final executado.

| Cenário | Investigação anterior | Execução final |
| --- | ---: | ---: |
| Commit genérico, 0 MiB | 5,01 ms | 36,49 ms |
| Commit genérico, 8 MiB | 69,11 ms | 112,87 ms |
| Commit genérico, 32 MiB | 245,88 ms | 233,58 ms |
| **Stream real**, 0 / 8 / 32 MiB não relacionados | não medido | **1,67 / 1,34 / 1,30 ms** |
| **Stream real**, sessão ativa com mais 4.096 eventos e 8 MiB não relacionados (índice aquecido) | não medido | **1,55 ms** |
| Reaplicar 32 partes idênticas, 8 MiB | 2.011,70 ms; 32 commits | 0,066 ms; 0 commits |
| Reconciliar 32 partes alteradas em lote, 8 MiB | não medido | 1,70 ms; 1 commit |

Alocações do stream real: ~3,9–4,1 kB por lote nas três cargas; commit
genérico de 8/32 MiB: ~60,9/160,1 MB. Tempos de fsync em Windows variaram
entre execuções, portanto diferenças pequenas não sustentam afirmação de ganho
no checkpoint genérico. O caminho medido `commitStreamUpdates` é o chamado pelo
worker do stream; o benchmark original `commitLocked` agora é custo residual.

## Pendências e riscos concretos para revisão

- `commitLocked` e checkpoint periódico ainda fazem snapshot completo e fsync
  segurando `app.mu`; filas, grafos, consultas e transições terminais continuam
  expostos a pausas dependentes do histórico. A reconciliação de histórico
  genérico só pula sessões sem alteração de timestamp/tamanho; não há índice
  transacional explícito para todos esses fluxos. O journal pode crescer durante
  até 5 min de streaming sem outro commit; checkpoint pode causar pausa.
- SSE multiplexado direciona wakes de stream, mas ainda percorre IDs de sessões
  e recalcula todos os resumos nos commits genéricos. Os snapshots do journal SSE
  ainda carregam o evento completo alterado, incluindo payload nativo; ferramentas
  de resultado muito grande podem continuar caras. Frame persistido está limitado
  a 256 MiB e uma reconciliação maior falha de forma read-only. Testar side chat,
  subagentes, graph requests e paginação com múltiplas sessões reais.
- Cache backend não invalida automaticamente alteração de configuração externa
  durante TTL. Cancelamento de um discovery já iniciado precisa ser validado nos
  três processos reais. Baseline OpenCode por turno não foi reduzido: o início
  atual usa o inventário nativo também para reconstruir uso acumulado; não há
  watermark de continuidade validado que autorize pular mensagens externas ou
  retomadas. Preferi conservar essa leitura a introduzir uma heurística de silêncio
  ou uma suposição de que o runtime retido contém todo o estado nativo.
- Operações otimistas de configuração serializam alterações concorrentes do
  mesmo controle; UI durante remount e respostas atrasadas exigem validação
  humana. Os pendentes usam `sessionStorage`: sobrevivem a reload na mesma aba,
  sem garantia se armazenamento estiver indisponível/cheio ou a aba for fechada.
  Rascunhos ainda não enviados mantêm o comportamento anterior em memória.
  Restaurar uma entrega incerta é decisão manual e pode criar novo input se o
  usuário reenviar com outro ID; `Retry delivery` conserva o ID idempotente.
- Ainda há rotas legadas com resposta HTTP sob `app.mu`; a leitura/seleção de
  grafo foi retirada desse caso, mas não houve migração de todos os handlers.
  O índice de recibos idempotentes não possui retenção, e a detecção de colisão
  com IDs legados faz varredura de eventos na aceitação de uma nova mensagem.
- Sem profiling da UI real, validação de WebView2, Focus/proxy, streaming
  prolongado, Playwright aberto, Stop com POST simultâneo ou modelos pagos.
  Nenhuma suíte completa, revisão adversarial ou migração real foi executada.

## Adendo após revisão corretiva

O contrato de reconexão descrito acima (repetir a mesma URL por EventSource)
estava incorreto para paginação: os cursores da consulta ficavam obsoletos. A
correção posterior fecha a conexão no erro e abre uma nova URL com revisões
atualmente aplicadas por sessão, com backoff e descarte da inscrição anterior.
Resets genuínos continuam invalidando páginas antigas. O adendo registra a
mudança sem substituir as medições e limitações históricas desta implementação.
Também foi corrigido o descarte do lote OpenCode quando a falha nativa aparece
somente no snapshot final; partes válidas e diagnóstico passam pelo flush.
O resultado e as lacunas restantes estão em `LATENCY_CORRECTIONS_RESULT.md`.

### Adendo R4

A implementação posterior de R4 substitui o checkpoint em cada comando por
transações incrementais no mesmo journal do streaming e checkpoint por segmento
fechado, fora de `app.mu`. O contrato atual, a migração/fence de downgrade e as
medições dos handlers estão em `LATENCY_R4_RESULT.md` e `engine/README.md`.
As limitações e medições anteriores acima ficam preservadas como histórico.
