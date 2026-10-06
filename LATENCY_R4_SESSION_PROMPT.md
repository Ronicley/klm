# Sessão preparada: R4 — persistência dos comandos (Sol High)

Configuração solicitada: sessão KLM normal, harness `opencode`, modelo
`openai/gpt-6-sol`, esforço `high`, no diretório registrado deste projeto.

Estado original: **prompt preparado; sessão KLM não criada**. O usuário autorizou a
criação com "Pode criar", após a proposta de uma sessão dedicada ao R4.
Na criação, usar o ID real dessa mensagem obtido pelo KLM como
`sourceUserEventId`; não inventar o ID. Operação estável reservada:
`latency-r4-sol6-high-v1`.

Continuidade: o usuário posteriormente pediu uso de subagente nativo. Este prompt
foi usado como especificação da task `ses_f14760debffeRX62Fg5aqVe4TH`, sem criar
sessão KLM. Implementação e revisão estão em `LATENCY_R4_RESULT.md` e
`LATENCY_R4_REVIEW.md`. A operação de criação KLM acima não foi utilizada.

## Prompt inicial

Implemente a conclusão do R4 da revisão de latência do KLM: persistência dos
comandos interativos e checkpoint global ainda bloqueiam a engine proporcionalmente
ao histórico de outras sessões. Você é uma sessão independente de implementação;
entregue código funcional e evidência direcionada, não apenas outro plano.

Diretório compartilhado: `C:\Users\Samuel\Documents\Projects\Personal\klm`.
Leia `AGENTS.md`, `product.md`, `LATENCY_IMPLEMENTATION_PLAN.md`,
`LATENCY_REVIEW.md`, `LATENCY_CORRECTIONS_RESULT.md` e
`LATENCY_CORRECTIONS_REVIEW.md`. A investigação detalhada está em
`LATENCY_INVESTIGATION.md`. Esses arquivos contêm o contexto; não é necessário
consultar outras sessões.

### Estado atual

- Streaming já usa um journal incremental durável, transporte SSE multiplexado,
  cache compartilhado e UI otimista. Preserve esses ganhos.
- R1 (restauração de rascunho), R2 (cursores atuais na reconexão) e R3 (diagnóstico
  na reconciliação final OpenCode) foram corrigidos e passaram nos checks focados.
- R4 foi deixado incompleto pela sessão anterior. `engine/store.go:commitLocked`
  ainda faz clone JSON de todo o estado, checkpoint/fsync e trabalho de histórico
  sob `app.mu`. `engine/stream_journal.go:checkpointStreamJournal` também grava
  o snapshot completo sob esse mutex.
- Envio/fila, claim/ack de steering, Stop, configurações, consultas/grafos e
  transições terminais continuam expostos a esse custo. Há também respostas HTTP
  em handlers legados executadas dentro do lock.
- Medições sintéticas da última revisão, sem/com 8 MiB de histórico alheio:
  aceite queue 3,30/115,44 ms; aceite steer 4,31/119,60 ms; YOLO 2,82/95,22 ms;
  Stop sem runtime 8,39/190,83 ms. Três iterações, não p95 de produção. Queue/steer
  medem aceite, não atuação do harness, e Stop não mede término de processo.

### Escopo obrigatório

1. Mapear os escritores e suas garantias antes da alteração; definir uma solução
   coerente de transações incrementais para comandos críticos e checkpoint
   separado. Documentar brevemente o contrato escolhido e implementá-lo.
2. Retirar clone/gravação do histórico global do caminho de cada comando crítico.
   Também evitar copiar integralmente uma sessão longa a cada pequena alteração.
3. Encurtar a seção crítica de `app.mu`; checkpoint, serialização e escrita HTTP
   não devem bloquear leitores e streaming durante todo o snapshot global.
4. Preservar ordem de revisões, aceitação durável, atomicidade de fila/consultas/
   grafos e recuperação após interrupção. Coordenar os caminhos genérico e de
   streaming; um escritor antigo não pode sobrescrever alterações recentes.
5. Preservar compatibilidade de leitura dos dados existentes e descrever eventual
   migração. Validar recuperação apenas em diretórios temporários. Não confirmar
   sucesso antes da durabilidade requerida, nem lançar goroutines de escrita sem
   protocolo de serialização e recuperação.
6. Manter Stop independente do envio, FIFO, identidades idempotentes, distinção
   queue/steer, entregas incertas sem replay automático, término nativo, contratos
   de Choice/MCP e retenção de runtime/Playwright.
7. Medir os caminhos realmente utilizados por envio, steering, configuração e
   conclusão, além do streaming. Não substituir benchmark por um caminho mais
   barato que produção não usa. Diferenciar a espera de aceitação da execução
   nativa e o custo amortizado de checkpoint de pausas na seção crítica.

A leitura integral do baseline OpenCode e profiling da UI são pendências separadas:
não ampliar esta sessão para uma otimização insegura desses fluxos nem declarar
essas pendências resolvidas pela mudança de persistência.

### Trabalho compartilhado e validação

Inspecione `git status` e os diffs antes de editar. O working tree tem alterações
de latência e outras alterações preexistentes (colaboração entre sessões, UI,
engine, documentação e distribuição). Preserve e integre esse trabalho; não
resete, limpe ou reverta arquivos de terceiros. Não faça commit/push, não gere
instaladores e não pare/reinicie a engine em uso.

Siga a política de `AGENTS.md`: não executar suíte completa nem montar infraestrutura
extensa de testes de integração. Use checks pequenos e necessários para ordem,
atomicidade, replay, checkpoint interrompido e interleaving stream/comando/checkpoint.
Não execute modelos pagos nem altere dados reais. Não crie sessões/subagentes ou
agentes de revisão adicionais.

Comandos de referência, adaptando o filtro apenas para os checks realmente afetados:

```powershell
# engine
go test -run '^Test(StreamJournalRecoveryAndNoOp|NewSubscriptionResetsAtCurrentRevision|StreamOwnsNativePayloadAndUsage|MessageIdentityReplay|MultiplexedStreamResumesObservedSession|OpenCodeFinalSnapshotErrorPersistsValidParts)$' -bench '^BenchmarkLatency(Investigation|Commands)$' -benchtime=3x -count=1 -benchmem

# clients/desktop, caso haja mudanças no contrato consumido pelo frontend
npx tsc -b --pretty false
npx vitest run src/features/chat/sessionState.test.ts src/features/chat/SessionOptions.test.tsx src/features/chat/MessageComposer.review.test.tsx src/features/chat/updatesConnection.test.ts
```

### Entrega

Criar `LATENCY_R4_RESULT.md` com decisão e formato de armazenamento, ordem de
gravação/publicação, recuperação/migração, arquivos alterados, comandos e resultados,
medições antes/depois e qualquer pendência concreta. Atualizar documentação de
contratos alterados sem apagar os resultados históricos das revisões.

Não marcar R4 como resolvido se comandos críticos ainda pagarem por todo o
histórico alheio sob o lock global. Se houver um bloqueio real, descrevê-lo com
evidências e a decisão necessária. Distinguir checks executados de inspeção e de
validação humana pendente.

Ao terminar, responder na própria sessão. Sessões KLM são independentes: não
monitorar, consultar ou notificar automaticamente a sessão orquestradora; o
usuário trará a conclusão para a próxima revisão.
