# Plano executável — conexões GitHub de webhooks por projeto

Status em 2026-10-12: **implementação A/B entregue; documentação C atualizada;
smoke local de API concluído; validação humana pendente**. Os owners relataram
sucesso nos checks Go/TS. A origem compilou um executável dev em diretório temporário
e executou uma checagem local curta com `KLM_DEV_DATA_DIR` isolado. A conferência
documental e esse smoke não equivalem à validação de todos os requisitos.
Este documento substitui o rascunho e conserva abaixo os contratos e o roteiro
planejados; o checklist de entrega distingue implementação de aceitação humana.

### Checklist de entrega e validação

- [x] A — Backend entregue: `engine/webhooks.go`, `webhooks_api.go`,
  `webhooks_worker.go`; hooks em `store.go`, `api.go`, `main.go` e
  `stream_journal.go`, incluindo validação após replay.
- [x] Check A: `go build -tags dev ./...` em `engine`, executado uma vez com
  sucesso **relatado pelo owner A**; nenhum teste executado.
- [x] B — Frontend entregue: Project settings General/Webhooks pela sidebar,
  General/ícone compartilhados, WebhooksSettings, DTOs/wrappers e wiring em
  App/WorkspaceSidebar/ProjectDialog, com CSS local.
- [x] Check B: `npx tsc -b` em `clients/desktop`, executado uma vez com sucesso
  **relatado pelo owner B**.
- [x] C — Documentação do comportamento implementado em `product.md` e
  `engine/README.md`: setup manual, HMAC, TTL, quotas, lifecycle e APIs de release.
  Conferência pontual dos contratos da seção 6 e do ponto de entrada na sidebar;
  nenhum mismatch bloqueador identificado nessa conferência. Checks de compilação
  não repetidos; nenhum arquivo Go/UI alterado por C.
- [x] Smoke de API pela origem: cadastro com DTO sem secret, assinatura inválida
  recusada, ping assinado aceito, duplicata com mesmo ID, liberação por cursor,
  payload original, lastPingAt e manutenção de payload/sequência/horário/deduplicação
  após stop/start do engine isolado. Nenhuma sessão/agente/grafo foi executado.
  A primeira tentativa após restart teve ECONNRESET por conexão keep-alive antiga
  no script; usando `Connection: close`, o cenário completo passou. Não foi
  necessária alteração do produto. O runtime temporário foi encerrado ao final.
- [ ] Validação humana de UI/layout/teclado/foco, criação/edição/remoção e múltiplas
  conexões/projetos (seção 10 e roteiro do README).
- [ ] Validação humana de ping/push assinados, Refresh/Verified, duplicatas,
  rejeições e entrega real GitHub via conexão/proxy/túnel do usuário.
- [ ] Validação humana de restart, pendentes/releases persistentes e paginação com
  cursor string independente por consumidor.
- [ ] Validação humana de TTL/calendário, quotas, disabled versus Removed,
  restauração e exclusão confirmada, usando dados descartáveis quando necessário.

Feed e automações continuam fora desta entrega. A seção 10 permanece como roteiro
de aceitação humana; UI, entrega real GitHub, recuperação de pendentes, TTL, limites
e lifecycle completo continuam sem validação humana. Nenhuma suíte foi executada.

## 1. Resultado e limites da entrega

Entregar configuração por projeto, recebimento GitHub assinado, persistência
atômica de evento/fila, liberação interna durável e retenção. Essa base precede
feed e automações; a ordem entre essas duas etapas futuras continua indefinida.

### Requisitos e decisões do usuário

- O botão **Project settings** da sidebar de sessions abre um modal com **General**
  e **Webhooks**, em vez do menu Edit/Remove. General mantém edição de nome,
  capacidades existentes de ícone e remoção do projeto.
- Configuração pertence ao projeto, não ao Settings global. Múltiplas conexões
  GitHub independentes, cada uma com ID, endpoint e secret próprios.
- Receber, validar, persistir evento e entrada da fila na mesma transação;
  confirmar ao emissor somente após gravação durável; liberar automaticamente
  em trabalho assíncrono, sem aguardar consumidores.
- **Emitido/liberado** significa disponível internamente em armazenamento durável.
  Não significa processado por feed, agente ou automação. Eventos continuam retidos.
- Futuros consumidores precisam de leitura por cursor e progresso independente;
  canal em memória e SSE não constituem entrega durável.
- Deduplicação por conexão + `X-GitHub-Delivery`.
- Retenção desde o recebimento: 1 dia, 3 dias, 1 semana, 1 mês, 3 meses, 6 meses,
  1 ano ou indefinidamente; meses/anos de calendário. Alterações só afetam novos
  eventos. Expiração remove também pendentes.
- Desativar recusa novas entregas, mas continua liberando as já aceitas.
  Excluir conexão, após confirmação explícita, apaga cadastro, eventos e fila.
- Manter remoção lógica/restauração do projeto com identidade e história preservadas.
- URLs usam o endereço do engine; sem campo de URL pública, proxy/túnel gerenciado
  ou PAT. Exposição externa é responsabilidade do usuário.
- HMAC-SHA256 obrigatório, payload/tipo/ação preservados; `ping` verifica recebimento.
  Preservar a bridge privada loopback e os limites de autorização de agentes/grafos.

### Escolhas técnicas desta implementação, não novos requisitos aprovados

As seções seguintes fecham contratos para os implementadores: nomes de campos,
rotas, formato de cursor, limites operacionais, secret informado pelo usuário e
política de pendentes em projeto removido. São escolhas do plano para entregar o
escopo acima, não decisões de produto atribuídas ao usuário.

Não incluir feed, automação, execução de grafos, GitHub App/OAuth, cadastro via API
do GitHub, PAT, broker externo ou infraestrutura de Tasks. Inspeção visual completa
de histórico/payload, sugerida no rascunho, não é requisito desta entrega. A API
paginada e o estado de verificação da conexão permitem validar a base entregue.

## 2. Código atual e consequências para a implementação

| Código inspecionado | Consequência |
| --- | --- |
| `clients/desktop/src/features/workspace/WorkspaceSidebar.tsx` | O botão usa `ProjectActionsMenu`; trocar por callback direto para o modal. |
| `features/projects/ProjectActionsMenu.tsx` e `ProjectDialog.tsx` | O menu também atende o rail. Preservar esses usos, criação de projeto, processamento de ícone e tratamento de erros. |
| `clients/desktop/src/App.tsx` | `editProject` e `removeProject` já atualizam inventário e navegação. Reutilizar callbacks e capturar o ID do projeto do modal. |
| `features/settings/SettingsDialog.tsx` | Há padrão de `<dialog>`, navegação lateral e painéis; reutilizar aparência/primitivas sem adicionar Webhooks ao Settings global. |
| `clients/desktop/src/engine.ts` e `platform.ts` | `request` concatena `ENGINE_URL`, exige JSON inclusive em respostas DELETE e aceita AbortSignal. Base respeita configuração, dev e cliente remoto. |
| `features/klm-agent/KlmAgentWorkspace.tsx` | Timeline usa `mockActivity`; Happening now e overlay usam sessões reais. Nenhuma integração de webhook nesse módulo nesta etapa. |
| `engine/api.go` | `decode` limita JSON a 512 KiB. Recebimento precisa de leitura bruta própria. Middleware já exige JSON, bloqueia mutações em shutdown/erro de storage e mantém CORS público. |
| `engine/store.go` | `diskState` é o estado persistido; `Project.Removed` é remoção lógica. `loadState` rejeita campos desconhecidos. `commitLocked` delega ao gate transacional. |
| `engine/transactions.go` | Clone reflexivo compartilha strings, mas copia slices/mapas. Diff de mapa substitui o valor inteiro por chave. Separar payload imutável de status mutável. |
| `engine/stream_journal.go` | Uma transação inteira por frame, CRC, `Sync` antes de publicar, limite de 256 MiB; replay rejeita corrupção completa e corta apenas cauda incompleta. Checkpoint a cada 5 minutos. |
| `engine/main.go` | Ordem: limpeza Tasks, load, replay, recuperação, criação do app, workers e HTTP. Shutdown usa contexto, `closing` e `wg`. |
| `engine/updates.go` | SSE é projeção de sessões/inventário, não log de webhook. `notifyAllLocked` já ocorre no commit, mas não fornece cursor de webhooks. |
| `engine/project_registration.go` | Restauração substitui o registro Project preservando ID e pastas. Guardar conexões separadamente por `projectId` evita perdê-las nessa substituição. |
| `engine/abandoned_tasks.go` | Remove `taskSchema`, `tasks`, `taskRuns`, `githubBindings`, `githubReceipts`, `engineTime`. Não reutilizar esses nomes nem restaurar código antigo. |

Há mudanças locais extensas, inclusive em App, engine.ts, main.go, estilos,
product.md e README, além do módulo novo klm-agent. Ler a versão corrente antes
de editar, aplicar patches locais e preservar essas mudanças. Não resetar,
reformatar arquivos inteiros, fazer commit ou push.

## 3. Modelo persistente mínimo

Usar o armazenamento existente, sob `app.mu` e `commitLocked`/
`commitTransactionLocked`. Não dividir aceitação entre arquivos independentes,
introduzir banco/broker ou escrever payload em disco fora da transação.

Adicionar campos opcionais novos ao `diskState`:

| Campo JSON | Conteúdo |
| --- | --- |
| `webhookConnections` | `map[string]WebhookConnection`, por ID da conexão. |
| `webhookEvents` | `map[string]WebhookStoredEvent`, por ID do evento; imutável até exclusão. |
| `webhookPending` | `map[string]WebhookPending`, por ID do evento, contendo apenas referência/sequência. |
| `webhookReleases` | `map[string]WebhookRelease`, por ID do evento, contendo `eventId`, `releaseSequence`, `emittedAt`. |
| `webhookReceivedSequence` | Contador `uint64` global de recebimento. |
| `webhookReleaseSequence` | Contador `uint64` global de liberação. |

Ausência de todos esses campos significa estado anterior válido/vazio. Inicializar
mapas de forma compatível; não exigir migração destrutiva nem mudar o journal format.
Contadores não diminuem nem reiniciam quando registros expiram ou são excluídos.

### Conexão

- `id`, `projectId`, `provider: "github"`, `name`, `enabled`, `secret`, `retention`.
- `createdAt`, `updatedAt`, `lastReceivedAt?`, `lastPingAt?`.
- `id` gerado no engine via `newID()`; caminho derivado dele e estável.
- Nome: `cleanLabel`, 1–60 caracteres. Identidade única é o ID, não o nome,
  repositório do payload ou caminho do projeto. Não inventar vínculo obrigatório
  com um repositório: um webhook de organização pode receber vários.
- Secret obrigatório informado individualmente pelo usuário, 16–512 bytes UTF-8,
  sem normalização/trim silencioso, sem caracteres de controle. Recomendar secret
  aleatório de alta entropia na documentação; comprimento sozinho não garante isso.
  A UI usa password, permite copiar o valor digitado e o descarta após salvar/fechar.
- Não copiar o secret de outra conexão por padrão. Troca via PATCH opcional;
  omissão mantém o atual, vazio/null é inválido. Não há período de dois secrets.
- O secret fica no storage privado do engine, assim como outras credenciais
  existentes, necessário para HMAC. Não prometer criptografia em repouso.
  Usar DTO explícito sem secret em toda resposta; nunca serializar a struct interna.
- `lastReceivedAt`/`lastPingAt` refletem somente aceitação durável, não tentativas
  inválidas nem duplicatas. `lastPingAt` é verificação histórica, não monitor online.
  Usar o maior receivedAt aceito para esses campos, evitando regressão com ingressos
  concorrentes que concluem o commit fora da ordem de leitura.
  Ao trocar secret, limpar `lastPingAt` para não afirmar verificação do novo valor.

### Evento e disponibilização

`WebhookStoredEvent`: `id`, `projectId`, `webhookId`, `provider`, `deliveryId`,
`eventType`, `action?`, `isPing`, `receivedAt`, `expiresAt?`, `receivedSequence`,
`payload` e `payloadBytes`.

- Guardar `payload` como **string imutável dos bytes JSON UTF-8 originais**, não
  `map[string]any` nem `json.RawMessage` mutável. Preserva bytes e números grandes,
  compartilha a string nos clones e evita regravar payload na liberação.
- Validar UTF-8/JSON antes de aceitar. Não compactar, truncar ou reserializar payload.
- Guardar somente identificadores necessários dos headers (`deliveryId`, `eventType`);
  não guardar Authorization, cookies, assinatura ou dump de headers.
- Não inventar timestamp de ocorrência: horários do GitHub continuam no payload.
  `receivedAt` é o horário UTC do engine quando terminou a leitura do body válido,
  capturado uma vez antes de entrar na região de commit; não é horário de emissão.
- Não gravar `emittedAt` dentro desse registro. A projeção consulta `webhookReleases`.
  Uma entrada está pendente OU liberada, nunca em ambos; `isPing` segue o mesmo fluxo.
- Fila não contém cópia de payload. Remover a entrada pendente e criar release são
  uma transação única; o evento permanece disponível até TTL/exclusão.
- Índice de deduplicação `(webhookId, deliveryId) -> eventId` e totais de bytes/itens
  podem ser caches no app, reconstruídos no startup e atualizados só depois do commit.
  São índices derivados, não a fonte de verdade. Não varrer payloads para paginar.

### Integridade e compatibilidade

Criar `validateWebhookRecords` para load, estado após replay e antes de save/checkpoint.
Validar referências a projetos/conexões, igualdade de projectId, mapas/chaves/IDs,
duplicatas de entrega retida, sequências únicas e menores/iguais aos contadores,
timestamps, retenção, tamanho do payload e relação pendente/liberado. Projeto
removido e evento expirado ainda não limpo são estados válidos. Não apagar registros
inconsistentes silenciosamente. Antes do commit, os mutadores validam os registros
alterados sem reparsear todos os payloads em cada comando de chat.

Preservar o fail-closed do armazenamento: falha de append/Sync aciona
`failStorageLocked`, não publica o novo estado nem retorna sucesso. O journal
continua incluindo evento, fila e contador no mesmo frame. Um binário antigo deve
recusar novos campos, nunca ignorar essas transações. Nenhum ajuste genérico em
`transactions.go` é necessário com os mapas separados e strings imutáveis.

## 4. Recebimento assinado e deduplicação

Endpoint público: `POST /api/webhooks/github/{webhookId}`. Aceitar apenas
`application/json` (parâmetros usuais de media type permitidos), sem compressão;
form-urlencoded ou Content-Encoding diferente de identity recebem 415.

1. Validar formato do ID e resolver conexão/projeto sob lock. Projeto ativo,
   conexão habilitada, engine aberto e storage saudável. Capturar o secret;
   soltar o lock antes da leitura/hash/parse.
2. Usar `http.MaxBytesReader` com **25 MiB (`25 << 20`)**; verificar também leitura
   chunked, sem confiar só em Content-Length. Excesso dá 413, nunca truncamento.
   Não usar `decode` genérico nem aumentar seu limite global de 512 KiB.
3. Exigir um único `X-Hub-Signature-256` no formato `sha256=` + 64 dígitos hex;
   HMAC-SHA256 sobre os bytes exatos; comparar digest com `hmac.Equal`.
   Ausência/invalidez dá 401. SHA-1, User-Agent e endpoint exclusivo não autenticam.
4. Exigir um único `X-GitHub-Delivery` (1–128 caracteres ASCII sem espaços/controles)
   e `X-GitHub-Event` (1–100, token ASCII). Não impor lista fechada de eventos.
   Parsear somente após HMAC. Exigir um objeto JSON, action ausente ou string
   até 100 caracteres; não exigir repository/sender/action em todos os tipos.
5. Reentrar em `app.mu`; rever closing/storage, existência, vínculo, Removed e
   enabled. Se o secret mudou enquanto a requisição estava em voo, retornar 409
   sem aceitar com a chave antiga. Revalidar retenção/limites atuais nesse lock.
6. Procurar a entrega na conexão. Se retida e não expirada, responder duplicata
   com o mesmo eventId, sem tocar timestamps, TTL, sequência, fila ou lastPingAt.
   Se o registro expirou, excluí-lo e seus índices na transação antes de aceitar
   a nova entrega. Outra conexão com o mesmo Delivery é independente.
7. Alocar sequência/ID, calcular expiresAt e persistir evento + pending + contador
   + metadados da conexão **em um único commit**. Atualizar caches após sucesso.
8. Responder 202 e acordar o worker sem esperá-lo. Handoff ao worker só depois do
   commit; uma liberação pode correr antes de os bytes da resposta chegarem ao
   GitHub, sem violar a separação entre aceitação e processamento.

Nova aceitação: `202 {"accepted":true,"duplicate":false,"eventId":"..."}`.
Duplicata: `200 {"accepted":true,"duplicate":true,"eventId":"..."}`.
Não prometer `emitted` nesse recibo. Erros seguem `{ "error": "English message" }`:
400 dados inválidos, 401 assinatura, 404 conexão/projeto inexistente ou removido,
409 conexão desativada/secret trocado, 413 excesso, 415 media/encoding,
503 capacidade/closing/persistência. Capacidade temporária inclui `Retry-After: 5`.

Deduplicação dura enquanto o evento estiver retido. Não guardar tombstones eternos
após TTL ou exclusão. A assinatura do GitHub autentica o body, não criptograficamente
os headers de tipo/delivery; preservar o contrato GitHub sem inventar assinatura
para headers. Um timeout após commit pode deixar o emissor incerto; redelivery
com mesmo ID deve recuperar o recibo. GitHub não garante redelivery automático:
documentar redelivery manual no GitHub após indisponibilidade, sem PAT no engine.

Buscar resposta em menos de 10 segundos conforme recomendação GitHub. Não confirmar
antes do Sync para atingir prazo. Manter os timeouts globais atuais; disco lento
pode produzir timeout/incerteza, resolvidos pela deduplicação, não por sucesso falso.

## 5. Worker, retenção e lifecycle

### Liberação

- Um worker do engine, independente de cliente/janela, registrado no `a.wg` e
  cancelável via `a.ctx`. Canal wake de capacidade 1 é apenas otimização.
- Primeiro ciclo no startup; fallback periódico a cada 1 segundo busca pendentes
  no storage. Um wake perdido ou reinício não deixa entradas paradas.
- Selecionar pendentes elegíveis por `receivedSequence` crescente, no máximo 100
  por commit; soltar lock entre lotes e verificar cancelamento. Sem sleeps por evento.
- Rever existência, expiração e projeto removido sob o mesmo lock de exclusão e
  aceitação. `enabled=false` não bloqueia pendentes.
- Atribuir `releaseSequence` global crescente e `emittedAt`; remover pending e
  gravar release na mesma transação. Contador de release distinto do de recebimento:
  liberações tardias não podem ficar atrás do cursor de um consumidor.
- Não chamar agentes, feed, sessões, bridge ou grafos. Não criar consumidor fictício,
  ACK de consumidor, retry de ação externa, leasing ou dead-letter queue nesta fase.
- Erro de storage mantém pendentes duráveis; respeitar estado read-only até reinício,
  evitando loop de erros. Após replay, entradas já liberadas mantêm sequência/horário.

### TTL

| Valor API | Rótulo UI | Cálculo UTC a partir de receivedAt |
| --- | --- | --- |
| `1d` | 1 day | 24 horas |
| `3d` | 3 days | 72 horas |
| `1w` | 1 week | 168 horas |
| `1mo` | 1 month | 1 mês de calendário |
| `3mo` | 3 months | 3 meses de calendário |
| `6mo` | 6 months | 6 meses de calendário |
| `1y` | 1 year | 1 ano de calendário |
| `forever` | Indefinitely | expiresAt ausente |

Seleção explícita no cadastro, sem default de retenção assumido. Meses/anos limitam
o dia ao último dia válido do mês destino: 31/jan + 1 mês = 28/29/fev;
29/fev/2024 + 1 ano = 28/fev/2025. Não usar AddDate diretamente sem esse clamp.
Persistir expiresAt uma única vez; edição/duplicata/emissão não recalcula eventos.

`expiresAt <= agora` significa expirado: oculto nas consultas e inelegível à
liberação mesmo antes da limpeza física. Limpar no startup antes de servir HTTP
e depois a cada minuto, em lotes de até 100: evento, pending/release e dedupe
derivado somem juntos. Usar helper de calendário/expiração com parâmetro `time.Time`
para validação local, sem endpoint público de relógio nem campo legado engineTime.

A remoção lógica via transação torna dados inacessíveis imediatamente; recuperação
de espaço físico ocorre pelo checkpoint/rotação existente (até o ciclo de 5 min,
quando saudável). Payload antigo ainda pode estar no journal/checkpoint anterior
até então. Não prometer apagamento seguro de bytes/backups nem manter cópia histórica
permanente dos eventos excluídos. Não apagar arquivo de journal fora do checkpoint.

### Projeto e conexão

- Excluir conexão: uma transação remove cadastro e todos seus eventos, pending e
  releases. Serialização pelo lock impede emissão/aceitação posterior à exclusão.
  Leitura já iniciada pode terminar com sua projeção anterior; não há revogação retroativa.
- Remover projeto mantém `Project.Removed`, IDs, conexões e eventos; preservar os
  atuais 409 para sessões/grafos ativos. Não fazer cascade de DELETE de conexão.
- **Escolha técnica do plano para Removed:** recusar novas entregas/CRUD/leitura
  pública desse projeto; suspender liberação de seus pendentes até restauração;
  continuar expiração. Worker pula essas entradas e atende outros projetos.
  Essa pausa não se aplica à simples desativação de uma conexão.
- Restauração reaproveita o ProjectID, mantém secrets/endpoints/enabled e libera
  pendentes ainda válidos. O fallback periódico já detecta restauração; não exige
  alterar `registerProjectLocked` nem acoplar restauração a canal volátil.
- Shutdown fecha admissão, cancela e aguarda worker; uma transação iniciada conclui
  via gate. Pendentes restantes retomam no próximo startup, sem pausa manual de chat.

## 6. Contrato API fechado para backend/frontend

Rotas novas usam `{id}` para projectId, conforme convenção atual de `api.go`.
Identificadores enviados pelo cliente são tratados como opacos/URL-encoded.
Todos os GETs e mutações gerenciais verificam projeto ativo e vínculo real da
conexão/evento. Nunca confiar no projectId do body ou do payload GitHub.

### DTOs públicos

```ts
type WebhookRetention = '1d' | '3d' | '1w' | '1mo' | '3mo' | '6mo' | '1y' | 'forever';
type WebhookConnection = {
  id: string; projectId: string; provider: 'github'; name: string;
  enabled: boolean; retention: WebhookRetention; secretConfigured: boolean;
  endpointPath: string; // /api/webhooks/github/{id}; nunca URL derivada de Host
  createdAt: string; updatedAt: string;
  lastReceivedAt?: string; lastPingAt?: string;
};
type WebhookEventMetadata = {
  id: string; projectId: string; webhookId: string; provider: 'github';
  deliveryId: string; eventType: string; action?: string; isPing: boolean;
  receivedAt: string; expiresAt?: string; receivedSequence: string;
  status: 'pending' | 'emitted'; emittedAt?: string; releaseSequence?: string;
  payloadBytes: number;
};
type WebhookPage<T> = { items: T[]; nextCursor?: string; hasMore: boolean };
```

Datas RFC3339Nano UTC; campos opcionais ausentes, não null. Coleções vazias são `[]`.
Sequências em **strings decimais** na API para não perder precisão em JavaScript;
armazenamento Go usa uint64. A API não expõe o diskState nem GraphRevision como
cursor de webhook. Status é projeção, não segundo estado persistido conflitante.

### Operações

| Método / caminho | Entrada | Resposta |
| --- | --- | --- |
| `GET /api/projects/{id}/webhooks` | `limit`, `cursor` opcionais | 200 `WebhookPage<WebhookConnection>` |
| `POST /api/projects/{id}/webhooks` | `{provider:"github",name,secret,retention}` | 201 `WebhookConnection`, inicialmente enabled=true |
| `PATCH /api/projects/{id}/webhooks/{webhookId}` | Campos opcionais `name`, `enabled`, `retention`, `secret`; ao menos um | 200 `WebhookConnection` |
| `DELETE /api/projects/{id}/webhooks/{webhookId}` | Sem body, após confirmação na UI | 200 `{removed:true}` |
| `GET /api/projects/{id}/webhooks/{webhookId}/events` | `limit`, `cursor` opcionais | 200 `WebhookPage<WebhookEventMetadata>` |
| `GET /api/projects/{id}/webhooks/{webhookId}/events/{eventId}` | — | 200 `{event:WebhookEventMetadata,payload:string}`; um payload por chamada |
| `GET /api/projects/{id}/webhook-events` | `after` decimal, default `"0"`; `limit` | 200 `{items:WebhookEventMetadata[],nextAfter:string,highWatermark:string,hasMore:boolean}`; somente liberados |
| `POST /api/webhooks/github/{webhookId}` | Headers e body originais do GitHub | Recibo 202/200 da seção 4 |

CRUD usa decode estrito existente; rejeita provider diferente, alteração de
projectId/id/provider, retenção desconhecida, null/vazio inválido e campos estranhos.
DELETE ausente retorna 404; cliente pode recarregar lista após resultado incerto.
Não implementar retry automático de POST gerencial após erro de transporte: atualizar
lista e deixar usuário resolver, evitando criar duas conexões por tentativa cega.

### Paginação e cursor durável

- `limit`: default 50, permitido 1–100; inválido dá 400, nunca resposta ilimitada.
- Conexões ordenadas por `(createdAt,id)` crescente; cursor opaco versionado contém
  esse último par e projectId. Eventos da conexão ordenados por receivedSequence
  decrescente; cursor contém último número, projectId, webhookId e versão.
  Limitar cursor a 1 KiB, validar formato/escopo e não usar índice de array/offset.
- Releases por projeto: `after` exclusivo, ordem crescente de releaseSequence.
  Sob lock capturar `highWatermark = webhookReleaseSequence` e selecionar só
  registros ainda retidos desse projeto entre after e highWatermark. Não incluir
  payload na página. after maior que contador atual ou inválido dá 400.
- Se há mais resultados retidos no intervalo, nextAfter é a sequência do último
  retornado e hasMore=true. Se terminou, nextAfter=highWatermark e hasMore=false,
  inclusive página vazia. A próxima leitura usa esse nextAfter; novas liberações
  sempre terão sequência maior, inclusive eventos recebidos antes e liberados tarde.
- Cursor continua utilizável quando a linha correspondente expirou/foi removida:
  lacunas são normais e não fazem a leitura reiniciar. TTL pode causar perda legítima
  para um consumidor atrasado; não prometer histórico completo, detecção de cada
  lacuna ou recuperação de payload expirado.
- Ler não consome nem altera evento, fila ou cursor global. Dois consumidores podem
  ler o mesmo intervalo independentemente. Persistência do progresso de cada
  consumidor será parte de feed/automação futuros, não tabela de consumidores vazia.
- Detalhe de evento expirado/excluído ou de outro projeto/conexão retorna 404.

## 7. Limites e baseline de segurança/integridade

Constantes iniciais de engenharia, documentadas no README, sem painel extra de quotas:

| Recurso | Limite inicial / comportamento |
| --- | --- |
| Payload individual | 25 MiB; 413 ao exceder. |
| Recebimentos lendo/hash/parse simultaneamente | 2 por engine, slot antes de ler body; excesso retorna 503, sem fila ilimitada de bodies. |
| Conexões | 100 por projeto e 1.000 por engine, incluindo projetos removidos; criação excedente dá 409. |
| Eventos retidos | 10.000 ou 256 MiB de bytes brutos por engine, incluindo ping, pendentes e projetos removidos; novo ingresso excedente dá 503. |
| Leitura paginada | 50 por padrão, máximo 100; listas sem payload. |
| Worker / limpeza | Até 100 eventos por transação; exclusão de conexão limitada pelo total global. |
| Headers e HTTP | Preservar MaxHeaderBytes=16 KiB e timeouts de main.go. |

Sem descarte silencioso de eventos não expirados para caber nesses limites;
`forever` não significa capacidade infinita. Duplicata retida retorna sucesso mesmo
com quota cheia, pois não ocupa espaço novo. Limpeza de expirados libera capacidade;
valores menores no futuro não autorizam apagar dados existentes. Conferir crescimento
da serialização: JSON-string pode expandir bytes; um body de 25 MiB deve continuar
cabendo no frame de 256 MiB com sua transação. Nunca aceitar um evento que não possa
ser persistido inteiro. Esta solução ainda carrega o estado em memória e faz
checkpoint completo; não anunciar escalabilidade ilimitada.

Proteger secrets e payloads com permissões usuais do diretório de dados/arquivos do
engine; não escrever no repositório do projeto, logs, SSE, mensagens de chat,
localStorage ou inventário. Erros de validação não devolvem body/secret/assinatura.
Payload é dado não confiável: não executar, interpolar em shell ou renderizar HTML.
Usar escape normal de React para metadados; não usar dangerouslySetInnerHTML.

HMAC autentica o recebimento, não toda a API administrativa. Preservar o contrato
atual de API pública pessoal sem autenticação de rede/TLS interno, sem ampliar sua
exposição por configuração automática. Documentar para exposição externa um proxy
HTTPS do usuário encaminhando somente a rota de recebimento necessária, mantendo
bytes/headers; não publicar administração do engine como parte desse exemplo.
Não adicionar allowlist de Host/CORS, serviço de IPs GitHub, rota de LAN para Stop
ou alterações em `loopbackHost`/bridge privada.

## 8. Interface mínima

- Criar `features/projects/ProjectSettingsDialog.tsx` com nav **General/Webhooks**,
  título **Project settings**, acessibilidade/fechamento/foco e visual do Settings.
  `WorkspaceSidebar` recebe `onProjectSettings(project)` e abre diretamente o modal.
- Estado de abertura guarda projectId; resolve projeto no inventário atual.
  Fechar se removido em outro cliente; troca de projeto não pode retargetear o form
  antigo. Cancelar/ignorar respostas antigas ao fechar ou mudar conexão/projeto.
- General reutiliza callback `editProject(id,{name,icon})`, maxLength=60 e preparação
  existente de imagem 96x96 PNG; diretório permanece fixo. Extrair componente/helper
  pequeno para compartilhar campos/ícone com ProjectDialog, preservando Add project.
- Remover usa o callback existente `removeProject(id)`, mantém bloqueio de submissão
  duplicada e mostra 409/erros no modal. Fechar só após sucesso. Não trocar remoção
  lógica do projeto pela exclusão destrutiva de suas conexões.
- Menu contextual do rail mantém Edit/Remove e ProjectDialog de edição. Alterar
  somente o ponto de entrada Project settings exigido, evitando remodelar o rail.
- Webhooks: lista paginada, **Add webhook**, edição de nome/retention/secret,
  enabled/disabled, endpoint com **Copy URL**, **Refresh** e remoção confirmada.
  Cadastro exige nome, secret e retenção. Provider GitHub fixo; sem picker fictício.
- Mostrar **Last received** e **Verified** com data de lastPingAt, ou **Not verified**.
  Não declarar Connected/online por existir cadastro ou sucesso do POST gerencial.
  Teste ocorre enviando ping pelo GitHub; não criar botão que finge verificar localmente.
- `Copy URL` usa `${ENGINE_URL}${endpointPath}`: preserva domínio, porta, prefixo
  configurado e dev. Não usar window.location.origin, porta 7332, Host/Forwarded
  recebido pelo servidor nem adicionar porta a domínio. Configuração existente do
  engine pode já ser remota; não criar um segundo campo de URL pública.
- Confirmar exclusão da conexão com **Remove webhook?**, resumo de que os eventos
  armazenados serão apagados e botões **Cancel / Remove webhook**.
- Buscar dados ao abrir Webhooks, após mutações e em Refresh. Sem novo SSE ou polling
  de background necessário. APIs de histórico/release não exigem tela de feed/log.
- Reutilizar Button/IconButton/Input/tokens; CSS local do recurso. Todo texto UI
  em inglês, inclusive rótulos de retenção e mensagens de erro. Responsivo no
  desktop/browser/WebView, sem mudanças Flutter/Tauri para esta funcionalidade.

## 9. Divisão executável para subagentes nativos

Esta é uma divisão para a sessão de origem executar depois de ler o plano.
Nenhum subagente ou sessão adicional foi criado para este planejamento.
Os subagentes devem receber o escopo, este caminho, contrato da seção 6,
ownership, instruções de preservação e proibições de commit/suítes em seus prompts.
Não herdam autorização para ampliar escopo, revisar adversarialmente ou executar grafos.

### A — Backend (owner exclusivo Go)

**Arquivos:** novos `engine/webhooks.go` (registros, validação, calendário e consultas),
`engine/webhooks_api.go` (CRUD/ingresso/DTO), `engine/webhooks_worker.go` (liberação e
retenção); hooks mínimos em `engine/store.go`, `engine/main.go`, `engine/api.go`.
Ownership de eventual ajuste indispensável em `transactions.go`/`stream_journal.go`
também é A, mas o desenho proposto não requer mudar seu formato/algoritmo.
`updates.go`, `abandoned_tasks.go`, project_registration e bridges são referência,
não destinos previstos de edição. Não tocar frontend/docs pertencentes a B/C.

**Ordem:**
1. Registros opcionais e validação de load/replay/save; mutadores transacionais,
   dedupe derivado, contadores e caches com atualização pós-commit.
2. CRUD e ingresso HMAC/limites/recibo após Sync.
3. Worker, TTL, shutdown e regras de Removed/disabled/exclusão.
4. DTOs e consultas/cursors da seção 6, sem payloads em inventário/SSE.

**Entrega à origem:** arquivos alterados, contrato efetivamente implementado,
diferenças inevitáveis do plano, resultado da compilação e pendências humanas.
Check único após concluir: `go build -tags dev ./...` em `engine`.

### B — Frontend (owner exclusivo UI/cliente)

**Arquivos:** `clients/desktop/src/App.tsx`, `features/workspace/WorkspaceSidebar.tsx`,
`features/projects/ProjectDialog.tsx` se necessário à extração; novos
`features/projects/ProjectSettingsDialog.tsx`, componentes pequenos de General,
`features/webhooks/WebhooksSettings.tsx`, `features/webhooks/api.ts` e CSS local.
Tipos DTO e wrappers ficam em `features/webhooks/api.ts`, importando `request` e
`ENGINE_URL` de `engine.ts`; evitar modificar esse arquivo central já alterado.
Não tocar features/klm-agent, Settings global, tokens/Menu genéricos ou backend.

**Ordem:**
1. Modal e General preservando criação/edição/ícone/removal; novo callback na sidebar.
2. Cliente tipado do contrato API; listagem paginada, CRUD e confirmação.
3. Endpoint/secret/retention, lastPingAt e loading/erro/cancelamento.

Pode trabalhar em paralelo com A a partir do contrato, sem inventar respostas
alternativas nem conectar mocks ao fluxo entregue. Integrar com dados reais antes
de declarar pronto. Check único após concluir: `npx tsc -b` em `clients/desktop`.

### C — Integração/documentação (começa após A/B entregarem)

**Arquivos exclusivos:** `product.md`, `engine/README.md` e atualização do status/checklist
deste `GITHUB_WEBHOOKS_IMPLEMENTATION_PLAN.md`. Não editar Go/UI em paralelo com A/B.
Se houver correção de wiring necessária, origem define transferência explícita de
ownership do arquivo depois de A/B terminarem; não corrigir o mesmo arquivo ao mesmo tempo.

**Tarefas:** conferir interfaces A/B contra seção 6, registrar o que realmente existe
em product.md e README, documentar setup GitHub JSON/secret/URL, retenção, limites,
redelivery e leitura dos liberados; preparar instruções manuais da seção 10.
Preservar integralmente as seções recentes de KLM Agent/Happening now e outras mudanças.
Não executar testes de integração/suítes ou revisão adversarial. Repetir compilação
apenas se alguma correção nova afetar o código já compilado; não duplicar checks por rotina.

**Sequência de coordenação:** origem lê este documento -> despacha A e B como
subagentes nativos -> recebe entregas e resolve divergências do contrato -> despacha
C -> apresenta resultado com checks realmente executados e validações humanas pendentes.
Feed e automações não são critérios para concluir A/B/C.

## 10. Aceitação e validação humana preparada

Os itens abaixo são roteiro de validação, não alegação de testes executados.
Usar engine dev/dados descartáveis para simular falhas/expiração; não adulterar o
state/journal do engine em uso. Para ingressos locais, assinar exatamente os bytes
UTF-8 enviados, variando Delivery; não colocar secrets reais em exemplos versionados.

1. Abrir Project settings pela sidebar, General/Webhooks; editar nome/ícone;
   confirmar criação e menu de edição do rail preservados. Remover projeto com
   sessão/grafo ativo mantém 409 e mostra o erro.
2. Criar duas conexões num projeto e uma em outro, com secrets distintos; endpoints,
   configurações, entrega e listagem ficam independentes. Observar ausência do
   secret nas respostas, /api/state, SSE e logs.
3. Enviar ping assinado: 202 após persistência, lastPingAt atualizado; Refresh mostra
   verificação. Enviar push sem action e tipo/ação novos; preservar bytes/payload.
4. Conferir HMAC ausente/incorreto, JSON/UTF-8 inválido, headers duplicados/ausentes,
   Content-Type errado e body acima do limite, inclusive chunked: nenhum evento/fila.
5. Repetir Delivery, inclusive concorrentemente, antes/depois da liberação: mesmo
   eventId, uma entrada, expiresAt intacto. Mesmo Delivery em outra conexão é novo.
6. Reiniciar depois de aceitação com pending persistido: worker retoma sem cliente;
   release já gravado mantém sequência/horário. Simular falha de escrita em dados
   descartáveis: sem 2xx nem publicação em memória; reinício/reenvio resolve incerteza.
7. Ler releases com after=0 e depois nextAfter, usando duas leituras independentes;
   nenhuma consome o evento. Paginar com exclusão/TTL no meio e conexão nova;
   liberação tardia aparece graças ao cursor de releaseSequence, distinto do recebimento.
8. Mudar retenção: expiresAt antigo permanece. Conferir helper UTC com 31/jan,
   29/fev, 3/6 meses e forever; expirado some mesmo se pending e antes da próxima
   limpeza. Após checkpoint saudável, não fica coleção permanente dos payloads apagados.
9. Desativar: novo POST é recusado, pending aceito ainda libera. Trocar secret invalida
   o antigo e limpa verificação; requisição concorrente não aceita chave obsoleta.
   Excluir conexão confirmada apaga cadastro/eventos/pending/releases; URL antiga falha.
10. Remover/restaurar projeto: conexões e história retidas, novas entregas recusadas,
    pending suspenso e TTL ativo; outros projetos continuam liberando. Restaurar
    mesmo diretório preserva identidade/URLs/secrets e retoma apenas pendentes válidos.
11. Limites de capacidade: recusar novos ingressos sem evicção, aceitar duplicata
    retida, permitir recuperação após TTL/exclusão. Slots de leitura são liberados
    também em erros/cancelamento. Cada resposta paginada respeita seu limite.
12. Validar desktop/browser/viewport compacto: modal, teclado, copiar URL, erros,
    resposta tardia após trocar projeto, Refresh e confirmação; URL usa base/porta
    do engine correta. Validar entrega real GitHub/LAN/proxy manualmente depois.
    Nenhum webhook dispara feed, sessão, agente ou grafo.

Durante implementação, executar apenas a compilação Go e o typecheck TS descritos
por ownership. Não rodar suítes completas, gerar testes de integração/complexos,
executar cenários destrutivos ou agentes de revisão sem pedido explícito. Nesta
tarefa de planejamento não há motivo para build: conferência é documental/código.

## 11. Fontes e limites do conhecimento

- `AGENTS.md`, `product.md` e arquivos do código relacionados na seção 2, lidos na
  versão de trabalho atual, incluindo alterações não commitadas.
- GitHub, payload/headers e teto de 25 MB:
  https://docs.github.com/en/webhooks/webhook-events-and-payloads
- GitHub, HMAC:
  https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
- GitHub, resposta em 10 segundos, secret e identidade de redelivery:
  https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks

O limite local de 25 MiB é escolha explícita compatível com o teto documentado de
25 MB, não citação de que GitHub define MB como MiB. Nenhum teste runtime, suíte,
agente de revisão, commit ou push faz parte da conclusão deste plano.
