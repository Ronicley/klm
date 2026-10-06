# Revisão da implementação de latência

## Veredito

**Entrega parcial; não aprovada como concluída.** A sessão
`ee8a9371ccd702683d443da496535885` (Implementação — latência de chat e engine
(Sol High)) terminou com `Execution stopped.` / `cancelled`, sem resposta final
de conclusão nas últimas mensagens consultadas. Há implementação substancial e
`LATENCY_IMPLEMENTATION_RESULT.md`, que também registra pendências.

Esta revisão foi feita no working tree compartilhado. Alterações de colaboração
entre sessões e outras mudanças que já existiam antes da implementação foram
tratadas como trabalho preexistente, não como defeitos introduzidos por esta entrega.

## Achados para a sessão corretiva

### R1 — P1: rascunho restaurado não pode ser enviado novamente

**Arquivos:** `clients/desktop/src/features/chat/MessageComposer.tsx:89-95` e
`clients/desktop/src/App.tsx:463-470`.

O composer grava o objeto enviado em `submittedDraft.current` e ignora envios
quando o rascunho é esse mesmo objeto. `Restore draft` restaura precisamente
`message.draft`, preservando a referência original. O botão fica habilitado,
mas o clique não chama `onSend`; é necessário editar o conteúdo ou remontar o
componente para sair desse estado.

**Reproduzido por teste direcionado:**
`clients/desktop/src/features/chat/MessageComposer.review.test.tsx`.
Enviar → limpar → restaurar o objeto original → enviar novamente.
Esperado: duas chamadas de envio. Observado: uma chamada.

**Correção:** deduplicar apenas o gesto repetido antes da atualização do editor,
sem bloquear uma restauração deliberada. Resetar corretamente a guarda ao mudar
de rascunho/ciclo de submissão. Manter o teste e fazê-lo passar; conferir também
menções e retorno após uma falha, sem duplicar clique/Enter acidentais.

### R2 — P1: reconexão automática usa cursores obsoletos e perde páginas carregadas

**Arquivos:** `clients/desktop/src/App.tsx:301-327`, `engine/updates.go:32-41,61-70`
e `clients/desktop/src/features/chat/sessionState.ts:50-56`.

Os cursores são capturados uma vez ao construir a URL de `EventSource`.
`onerror` só muda a mensagem de erro, deixando a reconexão automática reutilizar
a mesma URL. O servidor não emite `id:` nem aceita Last-Event-ID nesse novo
endpoint: lê exclusivamente os cursores da query.

Cenário concreto, rastreado no código:

1. Abrir uma sessão sem `history`; a URL é criada sem cursor para ela.
2. Receber reset, deltas e carregar páginas mais antigas.
3. Perder a conexão sem mudar as inscrições.
4. A reconexão envia novamente a query sem cursor, mesmo que o cliente tenha
   uma revisão atual válida.
5. O servidor força outro reset. O merge descarta páginas antigas de propósito
   no caminho de reset, perdendo a janela carregada e potencialmente o scroll.

Quando existe cursor inicial, ele também fica preso no valor antigo; depois de
atividade suficiente, pode expirar mesmo se o cursor atual do cliente for válido.
Isso contradiz a afirmação do relatório de que reconexão normal conserva paginação.

**Correção:** reconectar com os cursores atuais de cada sessão observada, com
backoff/cleanup e apenas uma conexão ativa, ou implementar um mecanismo equivalente
de retomada negociado pelo protocolo. Não "corrigir" mantendo páginas em todo
reset: um reset genuíno após expiração deve continuar invalidando páginas antigas.
Adicionar check focado de desconexão após hidratação/paginação e retomada válida.
Esse achado é de inspeção do protocolo/código, não de teste de rede real da aplicação.

### R3 — P2: falha encontrada na reconciliação OpenCode pode desaparecer do histórico

**Arquivos:** `engine/opencode_interactive.go:1248-1265,1302-1313` e
`engine/harness.go:558-560,594-602,349-360`.

A reconciliação ativa `p.finalBatch` e agenda um defer que só limpa o campo.
Se uma mensagem no snapshot final contiver `info.error`, o método retorna
`p.failure(...)` antes do flush. `failure` marca `p.failed`, mas seu `put` apenas
adiciona o evento de erro ao batch e retorna nil. O defer descarta esse batch.

No encerramento externo, `p.failed` muda a sessão para error sem gerar outro
evento com a causa. Assim, um erro encontrado apenas no snapshot final fica sem
mensagem diagnóstica; partes válidas acumuladas antes dele também são descartadas.
Outros retornos antecipados do bloco precisam de tratamento explícito do lote.

**Correção:** definir um caminho de finalização do batch para sucesso e erro.
Preservar partes válidas anteriores e a causa do erro sem persistir partes que
falharam na validação. Propagar falhas de gravação. Evitar um defer por iteração
do loop que apenas zera o campo. Verificar uma falha nativa descoberta no snapshot
final; não requer iniciar um modelo real.

### R4 — P1, lacuna de escopo: persistência de comandos e checkpoints continua global

**Arquivos:** `engine/store.go:307-335`, `engine/stream_journal.go:209-230`,
`engine/api.go`, `engine/message_queue.go` e `engine/harness.go`.

O hot path de texto melhorou, mas envio/fila, claim e confirmação de steering,
Stop, configuração e transições terminais ainda podem usar `commitLocked`:
JSON clone completo + checkpoint + fsync sob `app.mu`. O checkpoint periódico
também grava tudo segurando esse mutex. Portanto, a parte do plano que encurta
a seção crítica e remove custo global das interações ainda não foi concluída.

Medições desta revisão confirmam crescimento do commit genérico de ~6,90 ms
(0 MiB adicionais) para ~120,63 ms (8 MiB) e ~415,76 ms (32 MiB). São amostras
sintéticas de três iterações, com outro check de compilação rodando em paralelo;
não representam p95 nem demonstram regressão contra as amostras anteriores.
Mostram que o custo residual continua proporcional ao histórico não relacionado.

**Correção:** terminar a separação entre operações duráveis interativas e
checkpoint global, ou uma redução equivalente que elimine a dependência de
histórico não relacionado nos comandos críticos. Manter aceitação durável,
atomicidade e revisão ordenada, preservando contratos de fila/consultas/grafos.
Não mover gravações para goroutines sem protocolo de serialização/recuperação,
nem publicar sucesso antes da durabilidade necessária. Exercitar interleaving
entre stream, comando e checkpoint/replay com dados temporários.

Medir também os caminhos reais de envio/steering/configuração/conclusão, não
somente `commitStreamUpdates`. Se a lacuna não puder ser fechada nesta sessão,
registrá-la como incompleta, sem afirmar que o objetivo global foi concluído.

## Outras pendências de aceitação

- Baseline OpenCode ainda lê todo o histórico no início de cada turno. A sessão
  explicou o motivo (uso acumulado e continuidade), mas esse cenário longo ainda
  precisa de uma estratégia confiável de reutilização/cursor ou de medição que
  delimite o custo remanescente. Não suprimir leitura com uma heurística insegura.
- Persistem handlers que escrevem a resposta HTTP sob `app.mu`; priorizar os
  envolvidos em envio, seleção, Stop e respostas de ferramentas/interação.
- Faltam verificações dos fluxos integrados de Stop com envio pendente e
  HTTP/SSE fora de ordem na UI de submissões. Os testes existentes verificam
  merge de estado e toggle YOLO, mas não cobrem toda a coordenação no App.
- WebView2, Focus/proxy, harnesses reais e profiling de render continuam como
  validação humana pendente. Não foram aprovados por esta revisão de código.

## O que foi confirmado nesta revisão

- Há transporte multiplexado e remoção do SSE por runtime no frontend.
- O stream usa journal incremental e evita upserts idênticos.
- Cache de modelos é compartilhado e a descoberta backend não segura mais o
  mutex global durante todo o I/O do harness.
- Existem envio local pendente, identidade idempotente e previews de configuração.
- O ganho de custo do hot path e da reconciliação não é apenas uma afirmação do
  relatório: o benchmark foi reexecutado.

| Cenário | Medição desta revisão |
| --- | ---: |
| Stream real, 0 / 8 / 32 MiB não relacionados | 5,24 / 5,64 / 5,37 ms |
| Stream real, 4.096 eventos adicionais na sessão ativa | 4,75 ms |
| Reaplicar 32 partes idênticas | 0,187 ms; 0 commits |
| Reconciliar 32 partes alteradas | 4,41 ms; 1 commit |

Variação de fsync/carga local impede comparar pequenas diferenças de tempo entre
execuções. A redução de custo e alocação proporcional ao histórico no hot path
continua visível; não estender essa conclusão ao commit genérico.

## Checks executados

Em `engine`:

```powershell
go test -run '^Test(StreamJournalRecoveryAndNoOp|NewSubscriptionResetsAtCurrentRevision|StreamOwnsNativePayloadAndUsage|MessageIdentityReplay|MultiplexedStreamResumesObservedSession)$' -bench '^BenchmarkLatencyInvestigation$' -benchtime=3x -count=1 -benchmem
```

Passou: cinco testes focados e os benchmarks, com dados temporários.

Em `clients/desktop`:

```powershell
npx tsc -b --pretty false
npx vitest run src/features/chat/sessionState.test.ts src/features/chat/SessionOptions.test.tsx src/features/chat/MessageComposer.review.test.tsx
```

Typecheck passou. Vitest: **10 testes passaram e 1 falhou**, reproduzindo R1.
O teste novo foi deixado no working tree para orientar a correção. Não houve
alteração do código de produção pela revisão. `git diff --check` não encontrou
erros de whitespace (apenas avisos da política CRLF).

Não foi executada suíte completa, migração real ou teste pago com modelo.

## Encaminhamento

Sessão corretiva criada conforme autorização anterior do usuário:

- **Correções — revisão de latência (Sol High)**.
- ID: `e6add2846015af474b9bf7acc290847d`.
- Harness/modelo/esforço: `opencode` / `openai/gpt-6-sol` / `high`.
- Operação idempotente: `latency-review-corrections-sol6-high-a2b62409-v1`.
- Autorização de criação: mensagem `a2b62409e51821fde5681677b8384ccb`.
- A primeira chamada deu timeout; a repetição com a mesma operação e payload
  confirmou aceitação. O recibo confirma criação, não início ou conclusão.
- Entrega esperada: `LATENCY_CORRECTIONS_RESULT.md`, com R1–R4 e pendências.
- Não foi iniciado monitoramento nem coleta automática de resultados.
