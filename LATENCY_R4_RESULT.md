# R4 — transações incrementais e checkpoint separado

Data: 2026-09-29. **R4 implementado, com checks direcionados e medições dos
caminhos de produção.** A revisão global de latência ainda tem as pendências
de baseline OpenCode, UI/rede e validação nativa descritas ao final.

Execução como subagente nativo autorizado, no working tree compartilhado. Sem
subdelegação, commit/push, instaladores, modelos pagos, restart da engine ou uso
de dados reais. As alterações anteriores, inclusive R1–R3, foram preservadas.

## Decisão e mapa dos escritores

A solução mantém os callbacks transacionais existentes e o journal de streaming,
mas elimina o clone JSON/checkpoint global a cada comando. O formato de transação
é um conjunto de patches do esquema concreto de `diskState`, dentro de **um único
frame durável**. Não são gravações independentes por sessão/registro.

| Escritores | Caminho atual e garantias |
| --- | --- |
| POST queue/steer, remoção/promoção, claim/ack, recibos idempotentes | `commitLocked` → `commitTransactionLocked`. Fila, payload preparado, recibo e evento aceito continuam na mesma transação quando alterados juntos. Claim `sending` precede a tentativa nativa; ack remove payload/fila e acrescenta histórico atomicamente. |
| Stop, permissões, perguntas, modelo/esforço/YOLO, favoritos, native ID e metadados | Mesmo gate. Stop cancela antes da persistência, continua independente do POST e conserva espera por término nativo. Respostas HTTP saem depois de liberar `app.mu`, inclusive nos erros. |
| Consultas, spawn/side/subagentes, conclusão do turno | Mesmo gate; múltiplas sessões e projeções entram no mesmo frame. `finishTurnStateLocked` é a transação extraída do encerramento real de `execute`, sem antecipar settlement/drain/flush nativos. |
| Grafos, Choice, outbox do catálogo, notificações, shutdown | `graphChangeLocked` e os demais callbacks convergem no mesmo gate. Rejeição de domínio não publica estado nem torna a engine read-only. Validação relacional é mantida; snapshots capturados validados/compilados são reutilizados. |
| Texto/reconciliação e uso de tokens | Permanecem no caminho incremental anterior, no **mesmo arquivo e mesma sequência de revisões** dos comandos. No-op não grava. R3 permanece intacto. |
| Migração/interrupção/outbox em startup | `saveState` somente antes de servir requests. Lê/reaplica os journals antes da recuperação existente; não reexecuta modelos, ferramentas ou inputs incertos. |
| Checkpoint periódico | Único escritor live de `state.json`, coordenado por `checkpointMu`; reconstrução pesada fora de `app.mu`. O worker é aguardado no shutdown antes de liberar o diretório. |

Não existe mais um caminho genérico live que grave um snapshot global antigo por
cima de comandos/stream recentes. As rotas de authoring mantêm seu outbox e seu
journal de arquivos separados, agora com o intent e sua conclusão no journal de estado.

## Formato e ordem de publicação

- `state.json` continua v2, acrescido de `journalFormat: 1`. A nova engine lê
  v1/v2 antigos e registros stream-only. Antes de servir, grava o marcador para
  que binários anteriores, que usam `DisallowUnknownFields`, recusem o formato.
  **Downgrade não é suportado.** Migração foi exercitada somente em temporários.
- `stream.journal`: frames `uint32 LE tamanho`, `uint32 LE CRC32`, JSON; limite
  de 256 MiB por frame. Registros anteriores de stream/usage continuam legíveis.
- Comandos: `{format:1,revision,transaction:[{path,value?|length?|delete?}]}`.
  `path` é um array de nomes de campos (tag JSON ou nome Go quando não há tag,
  inclusive containers embutidos), índices de slices e chaves de mapas. `value`
  substitui o valor, inclusive limpando campos opcionais antigos; `length` redimensiona
  slices e `delete` remove uma chave. Não há merge implícito de objetos/pointers.
- Sob `app.mu`: preparar metadados privados → executar/validar callback → calcular
  apenas patches alterados e eventos anexados/substituídos → append + `Sync` + close
  → publicar estado/revisão → registrar mudanças SSE ordenadas e notificar.
- Strings permanecem compartilhadas como valores imutáveis. Históricos de sessão
  e ativação não são clonados. Sessões têm append e substituição explícita por
  `diskState.setEvent`; logs de ativação são append-only. Substituições só atingem
  o array publicado depois da durabilidade. Os escritores existentes foram adaptados.
  Janelas HTTP/export/SSE conservam seus próprios slices de eventos.
- Índices de IDs são preparados em startup e atualizados na publicação, evitando
  varrer históricos alheios no aceite de clientId e na validação de autorização
  de grafo. A validação/compilação de snapshots antigos também é aquecida em startup.
- Mudanças de ordem das sessões preservam o histórico **por ID** durante replay;
  não precisam gravá-lo novamente. Eventos novos saem ordenados por índice no SSE.
- Falha de journal não publica metadados, eventos ou sucesso; mantém o estado
  publicado anterior, cancela execuções e bloqueia novas mutações até restart.
  Uma queda depois de `Sync`, mas antes da resposta, ainda pode produzir aceite
  durável com resposta perdida: identidade idempotente e recuperação manual continuam
  sendo o contrato. Não há replay automático de entrega nativa incerta.

## Checkpoint, queda e interleaving

1. `checkpointMu` permite um checkpoint por vez. Com `app.mu`, mover o journal
   ativo para `stream.sealed` usando o replace durável da plataforma. Se já existe
   segmento fechado recuperável, processá-lo primeiro. Soltar `app.mu`.
2. Carregar o checkpoint anterior e aplicar somente `stream.sealed` em um estado
   independente. Enquanto isso, comandos e streaming escrevem um novo journal ativo.
3. Validar, serializar, escrever/sync/replace `state.json`, tudo fora de `app.mu`.
   Remover somente `stream.sealed` depois do replace. Falha entra no mesmo modo
   read-only; o journal ativo não é truncado nem removido por esse checkpoint.
4. Startup aplica checkpoint → sealed → ativo. Revisões já cobertas são ignoradas;
   revisões novas precisam ser contíguas. Uma cauda incompleta é truncada e synced;
   frame completo corrupto, formato desconhecido ou gap interrompe recuperação.

Isso cobre queda depois da rotação, escrita de novos comandos durante o checkpoint,
queda antes do replace e queda entre replace e remoção. Um frame parcialmente
gravado não publica metade de uma fila/consulta/grafo. O checkpoint continua a
cada cinco minutos; o custo é amortizado, não eliminado.

## Arquivos principais

- **Protocolo:** `engine/transactions.go`, `store.go`, `stream_journal.go`, `main.go`.
- **Integração dos escritores:** `harness.go`, `graph_runtime.go`, `graph_records.go`,
  `graph_tools.go`, `linked.go`, `subagents.go`, `message_identity.go`, `stream.go`.
- **HTTP fora do lock:** `api.go`, `message_queue.go`, `permissions.go`,
  `questions.go`, `models.go`, `permission_policy.go`, `transcription.go`,
  `graph_activity_api.go`, `authoring.go` e handlers de `linked.go`.
- **Evidência:** `transactions_test.go`, `latency_investigation_bench_test.go`.
- **Contrato:** seção Persistence And Recovery de `engine/README.md` e adendo em
  `LATENCY_IMPLEMENTATION_RESULT.md`. Resultados históricos não foram substituídos.

## Checks executados

Todos os dados dos testes/benchmarks ficaram em `testing.TempDir`, com TEMP/TMP
apontando para `C:\Users\Samuel\AppData\Local\Temp\opencode`.

```powershell
# engine — execução final com benchmarks
$env:TEMP='C:\Users\Samuel\AppData\Local\Temp\opencode'
$env:TMP=$env:TEMP
go test -run '^Test(TransactionRecoveryCheckpointInterleaving|TransactionFailureAndPartialTail|TransactionReplacementAndNewSession|TransactionConcurrentCheckpoint|TransactionLegacyMigration|TransactionHTTPOutsideLock|StreamJournalRecoveryAndNoOp|NewSubscriptionResetsAtCurrentRevision|StreamOwnsNativePayloadAndUsage|MessageIdentityReplay|MultiplexedStreamResumesObservedSession|OpenCodeFinalSnapshotErrorPersistsValidParts)$' -bench '^BenchmarkLatency(Investigation|Commands|Checkpoint)$' -benchtime=3x -count=1 -benchmem
```

**Passou: 12 testes focados e benchmarks; 4,384 s.** A mensagem `state persistence
failed ... is a directory` é a falha de escrita deliberada do teste, não uma falha
da execução. Checks cobrem:

- transação envolvendo fila/payload/recibo, atividade de grafo, consulta e seus
  eventos nas duas sessões; claim recuperável antes do ack;
- comandos + stream entre rotação e materialização do checkpoint, incluindo
  reordenação de sessões; checkpoint sobreposto sem reaplicar eventos;
- concorrência real entre goroutines de comando, stream e checkpoint, sem perda;
- falha de armazenamento sem publicação, rejeição de domínio, cauda parcial e CRC;
- substituição de opcionais, nova sessão com histórico vazio e ordem dos deltas SSE;
- leitura/migração de v1/v2 + journal anterior em temporários;
- `ResponseWriter` que verifica lock livre nos caminhos de configuração, favorito,
  erro de fila e Stop; seis regressões anteriores de stream/SSE/identidade/R3.

Também foram usados `gofmt`, execuções intermediárias do recorte durante a
implementação e `git diff --check`. Não houve suíte completa nem revisão adversarial.
Após o último ajuste de startup (aquecer validação antes de servir e iniciar o
worker somente depois do control channel), o mesmo filtro dos 12 testes foi
reexecutado com `-count=1`, sem benchmarks: **passou em 2,123 s**. O diff check
passou, com apenas os avisos locais de LF/CRLF.
Frontend não foi alterado e não houve mudança de API consumida por ele: não foram
reexecutados typecheck/Vitest. Os resultados anteriores de R1/R2 foram preservados.

## Medições finais

Windows amd64, Ryzen 7 8700G. **Três iterações por cenário**, médias diagnósticas;
não p95, tráfego real ou tempo de atuação do modelo. Baseline dos quatro handlers:
`LATENCY_CORRECTIONS_REVIEW.md`. As diferenças pequenas de fsync entre execuções
não sustentam comparações finas.

| Caminho | Antes 0 / 8 MiB | Depois 0 / 8 / 32 MiB |
| --- | ---: | ---: |
| POST real queue com turno reservado | 3,30 / 115,44 ms | **2,58 / 2,54 / 2,21 ms** |
| POST real steer com turno reservado | 4,31 / 119,60 ms | **2,04 / 2,68 / 2,01 ms** |
| Handler YOLO | 2,82 / 95,22 ms | **2,39 / 2,32 / 2,35 ms** |
| Handler Stop sem runtime e sem mudanças pendentes | 8,39 / 190,83 ms | **0,073 / 0,062 / 0,075 ms** |
| Stop com fila pendente, sem runtime | não medido | **2,26 / 1,97 / 1,97 ms** |
| Claim + ack de steering, dois commits | não medido | **3,87 / 3,65 / 3,72 ms** |
| Handler modelo/esforço com catálogo aquecido | não medido | **2,59 / 2,57 / 2,11 ms** |
| Transação terminal chamada por `execute` | não medido | **1,97 / 2,02 / 2,42 ms** |
| Commit genérico de alteração de evento | 4,31 / 106,14 ms¹ | **2,09 / 2,37 / 2,10 ms** |
| Stream de produção | já incremental | **2,30 / 2,13 / 2,32 ms** |

¹ Baseline do commit genérico em `LATENCY_CORRECTIONS_RESULT.md`; 32 MiB eram
305,45 ms. O benchmark foi adaptado somente para usar o setter transacional
obrigatório, conservando a mesma alteração do evento.

Outros casos: YOLO com **4.096 eventos na própria sessão** = 2,05 ms;
stream nessa sessão longa = 2,43 ms; 32 partes idênticas = 0,105 ms e **zero commits**;
32 partes alteradas = 2,53 ms e **um commit**. Alocação de stream: **4.056 B/op**
em 0/8/32 MiB. Queue: **59.432 B/op** nos três tamanhos. Conclusão: aproximadamente
16 KiB/op nos três tamanhos, em vez de clonar o histórico global.

### O que as fixtures realmente medem

- POST passa por decode, preparo, identidade, validação, aceite durável e escrita
  HTTP reais. Um turno reservado impede iniciar harness; não se mede atuação nativa.
- Claim/ack chama `takeSteering` e `finishSteering`, usados pelos adapters reais.
  A preparação da fila ocorre fora do cronômetro; não há acknowledgement de harness
  real nem afirmação de que um modelo recebeu/atuou sobre o texto.
- Modelo/esforço usa o handler real com catálogo sintético no cache. Inclui sua
  validação e persistência; não inclui descoberta externa de catálogo.
- Conclusão usa o mesmo `finishTurnStateLocked` chamado por `execute`, depois das
  barreiras nativas. O benchmark mede a persistência terminal; não mede espera por
  EOF, subprocessos, MCP ou reconciliação do harness.
- Stop sem pendências agora é no-op durável: não há novo estado a gravar. O caso
  adicional com fila mede uma alteração real para `paused`. Ambos não têm runtime,
  portanto não medem término de processos.

### Custo separado de checkpoint

| Histórico alheio | Checkpoint completo/op | Máxima espera amostrada de leitor por `app.mu` |
| --- | ---: | ---: |
| 0 MiB | 15,73 ms | 4,13 ms |
| 8 MiB | 66,93 ms | 4,05 ms |
| 32 MiB | 247,44 ms | 3,30 ms |

O leitor concorrente faz lock/unlock e amostra a cada 1 ms. Sua janela inclui os
pequenos commits usados para preparar cada checkpoint, portanto o máximo inclui
fsync de comando e agendamento; **não é a duração exclusiva da rotação nem p95**.
O tempo de checkpoint inclui carregar/reaplicar/validar/serializar/sync/replace e
remover o segmento. Há alocação global nessa tarefa (aprox. 73,4/322,4 MB alocados
por operação em 8/32 MiB), porém ela não mantém `app.mu` durante esse trabalho.

## Limites concretos e pendências

- Metadados ainda são copiados/comparados/validados por registro sob `app.mu`.
  Número muito grande de sessões, recibos, consultas e registros de grafo pode
  aumentar esse custo. Esta entrega remove a dependência do **histórico de eventos
  alheio** e de sua serialização, não promete custo constante para todo o banco.
  Recibos idempotentes continuam sem retenção.
- Append pode ocasionalmente realocar o array da própria sessão; isso é amortizado,
  não uma cópia integral em cada pequena mudança. Eventos novos/modificados grandes
  ainda têm custo próprio de serialização/SSE; o limite de frame permanece 256 MiB.
- O fsync de cada pequeno frame continua no gate ordenado e pode demorar por disco,
  antivírus ou pressão do sistema. Checkpoint disputa CPU/I/O e usa memória; journal
  e replay podem crescer até o próximo checkpoint. Não há promessa de latência fixa.
- O schema de patches é interno e versionado: novos escritores devem usar o setter
  para substituir eventos e respeitar logs de ativação append-only. Alteração desse
  contrato requer atualizar formato/replay juntos, não uma escrita paralela legada.
- Baseline OpenCode ainda percorre histórico nativo no começo do turno. Não foi
  inserida heurística de silêncio/cursor não confiável, nem declarado resolvido.
- Continuam pendentes validações humanas com Pi/Codex/OpenCode, término de processos
  de Stop, POST concorrente integrado à UI, Choice/MCP externo, runtime/Playwright,
  WebView2/Focus/proxy, reconexão real e render. Persistência não comprova esses fluxos.

**Conclusão para revisão:** os comandos críticos usam transações incrementais
duráveis, o checkpoint global foi retirado da seção crítica longa, e as medições
reais deixaram de crescer de 0 para 8/32 MiB de histórico não relacionado.

## Adendo — fixtures de persistência apontados na revisão

A revisão acrescentou `TestPermissionDecisionPersistence` e
`TestSubagentSessionsPersistBesideSideAgent` ao recorte anterior e encontrou falhas
nos dois. Os fixtures criavam `app.state` diretamente e depois usavam somente
`loadState`, assumindo que cada comando regravava `state.json`. Essa falha isolada
não demonstrava perda de dados no caminho de recuperação da engine.

Correção em `engine/permission_persistence_test.go` e `engine/subagents_test.go`:

- salvar o checkpoint inicial antes das mutações, como o startup;
- recuperar com `loadState` seguido de `replayStreamJournal`, sem checkpoint final;
- preservar todas as asserções anteriores de entrega, escopo de permissões,
  identidades, estado terminal e relacionamento main/side;
- conferir também os IDs, papel, parent e estado dos dois subagentes recuperados,
  além do evento terminal recuperado no histórico do parent.

O replay passou com esses fixtures; não foi necessária alteração de produção.
Nesta retomada foi executado somente o filtro solicitado, em temporários:

```powershell
$env:TEMP='C:\Users\Samuel\AppData\Local\Temp\opencode'
$env:TMP=$env:TEMP
go test -run '^Test(PermissionDecisionPersistence|SubagentSessionsPersistBesideSideAgent)$' -count=1
```

**Resultado: PASS, 0,968 s**, incluindo os sete casos de decisão de permissão.
`gofmt` aplicado aos dois testes. O recorte consolidado e os benchmarks não foram
reexecutados nesta retomada; ficam para a revisão solicitante. Sem dados reais,
commit ou push.
