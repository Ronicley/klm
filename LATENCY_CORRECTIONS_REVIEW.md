# Verificação da sessão corretiva de latência

## Veredito

**R1–R3 corrigidos no código e nos checks direcionados. R4 permanece aberto;
o objetivo global de latência ainda não está concluído.**

Esta verificação usou a entrega no working tree compartilhado e o relatório
`LATENCY_CORRECTIONS_RESULT.md`. Não houve alteração de código de produção nesta
verificação, execução de suíte completa, uso de modelos ou migração de dados reais.

## Conferência dos achados

| Achado | Verificação |
| --- | --- |
| R1 — reenvio de rascunho restaurado | `MessageComposer.tsx:38-41` libera a guarda quando o editor passa a outro rascunho. O teste que falhava na revisão anterior agora passou, assim como o caso com menções e clique repetido. |
| R2 — cursores na reconexão SSE | `updatesConnection.ts` fecha a conexão com erro e cria outra após backoff, consultando os cursores atuais. `App.tsx:304-305` fornece esses valores por callback. Checks cobrem atualização do cursor, troca de inscrição e cleanup; o reducer distingue retomada de reset genuíno. |
| R3 — erro perdido no lote final | `harness.go:575-589` finaliza o lote mesmo quando a reconciliação retorna erro. `opencode_interactive.go:1251-1309` usa esse caminho e interrompe corretamente após falha nativa. O check verifica partes válidas, diagnóstico recuperado pelo journal e falha de persistência sem publicação. |
| R4 — persistência global | `store.go:308-335` ainda clona/grava o estado global sob `app.mu`; o checkpoint periódico em `stream_journal.go` também mantém o lock. A retirada de escrita HTTP do lock em modelo/YOLO é uma melhora parcial, mas não resolve o achado. |

Os checks de R2 usam conexão simulada e merge de estado; não equivalem a
validação de reconexão real em WebView2/Focus/proxy. O check de R3 exercita o
caminho de lote e recuperação sem executar OpenCode real.

## Checks executados nesta verificação

Em `clients/desktop`:

```powershell
npx vitest run src/features/chat/sessionState.test.ts src/features/chat/SessionOptions.test.tsx src/features/chat/MessageComposer.review.test.tsx src/features/chat/updatesConnection.test.ts
npx tsc -b --pretty false
```

Resultado: **14 testes passaram em quatro arquivos; typecheck passou**.

Em `engine`, com TEMP/TMP apontando para o diretório temporário aprovado:

```powershell
go test -run '^Test(StreamJournalRecoveryAndNoOp|NewSubscriptionResetsAtCurrentRevision|StreamOwnsNativePayloadAndUsage|MessageIdentityReplay|MultiplexedStreamResumesObservedSession|OpenCodeFinalSnapshotErrorPersistsValidParts)$' -bench '^BenchmarkLatencyCommands$' -benchtime=3x -count=1 -benchmem
```

Resultado: **seis testes focados e benchmarks passaram**.

## Medição do custo residual de R4

Médias diagnósticas de três iterações por cenário em Windows/amd64, Ryzen 7 8700G.
Estado sintético em diretórios temporários. Os checks frontend já haviam terminado
quando este benchmark foi iniciado. Valores não são p95 nem medição de produção.

| Handler | Sem histórico alheio adicional | Com 8 MiB adicionais |
| --- | ---: | ---: |
| Aceite de mensagem queue | 3,30 ms | 115,44 ms |
| Aceite de mensagem steer | 4,31 ms | 119,60 ms |
| Configuração YOLO | 2,82 ms | 95,22 ms |
| Stop sem runtime | 8,39 ms | 190,83 ms |

Os cenários queue/steer medem preparo e aceite durável com turno fictício para
impedir despacho de harness; não medem claim/ack nem atuação do modelo. Stop não
mede término de processos nativos. A conclusão sustentada é que esses handlers
continuam pagando pelo histórico de outras sessões.

## Próxima etapa necessária

Concluir R4 com persistência incremental dos comandos críticos e checkpoint
fora da seção crítica longa, mantendo ordenação, atomicidade, recuperação e
aceite durável. Ainda restam a leitura integral do baseline OpenCode por turno
e validações reais de Stop/POST concorrentes, harnesses, render e rede.

Nenhuma nova sessão foi criada nesta verificação. Uma nova delegação exige
pedido explícito do usuário; a sessão corretiva anteriormente autorizada já
foi criada e sua entrega foi conferida aqui.
