# Revisão da entrega R4

## Veredito

**R4 implementado e verificado no recorte de código/testes/benchmarks.** Os
comandos críticos deixaram de clonar e gravar o histórico global a cada operação;
o checkpoint global foi retirado da seção crítica longa de `app.mu`.

A implementação foi delegada ao subagente nativo expressamente autorizado pelo
usuário (`ses_f14760debffeRX62Fg5aqVe4TH`) e depois revisada nesta conversa.
Não foi criada uma nova sessão KLM nem selecionado modelo via ferramenta Task.

## Pontos conferidos

- `commitLocked` e `graphChangeLocked` convergem para uma transação incremental
  durável. A publicação acontece após append/Sync do frame completo.
- Escritores de eventos existentes usam substituição transacional; apêndices e
  atualizações do SSE conservam ordem. A recuperação preserva histórias por ID
  quando a lista de sessões é reordenada.
- Fila, payloads, recibos idempotentes, consultas e dados de grafo podem ser
  recuperados juntos; o teste inclui claim de steering antes do ack.
- Checkpoint fecha um segmento sob coordenação curta e reconstrói o estado fora
  de `app.mu`. Novas operações continuam no journal ativo. Startup reaplica o
  segmento fechado e o ativo, ignorando revisões já consolidadas.
- Falha de escrita, cauda parcial, corrupção completa, substituição de opcionais,
  compatibilidade anterior e concorrência stream/comando/checkpoint têm checks
  direcionados. O worker de checkpoint é aguardado antes de liberar o diretório.
- Respostas HTTP dos caminhos testados não seguram o lock de estado durante a
  escrita. O frontend e as correções R1–R3 foram preservados.

## Ajuste encontrado durante a revisão

Dois testes antigos ainda pressupunham que cada comando atualizava `state.json`:
`TestPermissionDecisionPersistence` e `TestSubagentSessionsPersistBesideSideAgent`.
Falharam na primeira execução ampliada do recorte, pois criavam estado só em
memória e recuperavam apenas o checkpoint, sem replay.

O mesmo subagente ajustou os fixtures para gravar a base inicial e recuperar
checkpoint + journal, como o startup real. As asserções de permissões foram
mantidas e as de identidades/relacionamentos de subagentes foram ampliadas.
Não foi forçado checkpoint final nem alterada produção para mascarar os testes.

## Verificação final executada pelo orquestrador

Em `engine`, usando TEMP/TMP no diretório temporário aprovado:

```powershell
go test -run '^Test(TransactionRecoveryCheckpointInterleaving|TransactionFailureAndPartialTail|TransactionReplacementAndNewSession|TransactionConcurrentCheckpoint|TransactionLegacyMigration|TransactionHTTPOutsideLock|StreamJournalRecoveryAndNoOp|NewSubscriptionResetsAtCurrentRevision|StreamOwnsNativePayloadAndUsage|MessageIdentityReplay|MultiplexedStreamResumesObservedSession|OpenCodeFinalSnapshotErrorPersistsValidParts|PermissionDecisionPersistence|SubagentSessionsPersistBesideSideAgent)$' -bench '^BenchmarkLatency(Commands|Checkpoint)$' -benchtime=3x -count=1 -benchmem
```

**PASS — 14 testes direcionados, incluindo seus subcasos, e benchmarks; 4,461 s.**
A mensagem de falha de armazenamento no teste de erro é deliberada e esperada.
Não foi executada suíte completa. Não houve alteração frontend nesta etapa;
os checks frontend já executados na revisão anterior não foram repetidos.

## Medições desta revisão

Windows/amd64, Ryzen 7 8700G; três iterações por cenário, em dados temporários.
São médias diagnósticas, não p95 nem latência de harness/modelo real.

| Caminho | Sem histórico adicional | 8 MiB alheios | 32 MiB alheios |
| --- | ---: | ---: | ---: |
| POST queue, aceite durável | 2,03 ms | 2,11 ms | 2,25 ms |
| POST steer, aceite durável | 1,93 ms | 2,15 ms | 2,27 ms |
| YOLO | 2,18 ms | 2,26 ms | 2,54 ms |
| Claim + ack de steering | 3,74 ms | 3,75 ms | 3,73 ms |
| Stop com fila, sem runtime | 1,97 ms | 2,05 ms | 2,03 ms |
| Modelo/esforço, catálogo aquecido | 2,26 ms | 2,36 ms | 4,07 ms |
| Persistência terminal de execute | 2,39 ms | 2,17 ms | 2,09 ms |

O baseline com 8 MiB era 115,44 ms para POST queue, 119,60 ms para POST steer e
95,22 ms para YOLO. A diferença pequena entre amostras atuais não sustenta
afirmações finas; a remoção do custo de serializar o histórico alheio é visível
também nas alocações, que ficam constantes nos três cenários de envio.

O checkpoint completo levou 69,78/238,93 ms com 8/32 MiB, enquanto a máxima
espera amostrada dos leitores pelo mutex foi 3,09/3,14 ms. Essa janela também
inclui pequenos commits de preparação e agendamento; não é p95 nem medição
exclusiva da rotação. Stop sem runtime não mede término de processos; claim/ack
não mede confirmação ou atuação de harness real.

## Limites restantes

Metadados ainda crescem com o número de registros; fsync, checkpoint e memória
dependem do ambiente. Não há promessa de custo constante para qualquer volume.
O marcador de formato e a recuperação anterior estão descritos em
`LATENCY_R4_RESULT.md`; downgrade para binário que desconhece o journal não é
suportado. Não houve migração com dados reais.

Baseline integral OpenCode, execução real de Stop/Choice/MCP/Playwright, UI sob
carga e WebView2/Focus/proxy continuam pendentes de investigação/validação humana
conforme o escopo de cada item. O fechamento do R4 não declara esses fluxos
verificados. Nenhum instalador foi gerado nem a engine em uso foi reiniciada.
