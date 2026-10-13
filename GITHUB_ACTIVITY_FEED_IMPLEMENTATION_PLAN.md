# Plano executável — feed real de atividade GitHub

Status: **implementação entregue em 2026-10-13; Go/TS, smoke API descartável e Playwright pontual passaram; aceitação humana/externa pendente**.
Base inspecionada: branch `release/0.3.0`, incluindo os três arquivos locais modificados pelo agente pai.
Execução: o segundo subagente implementou diretamente, sem novas delegações, sessões ou grafos. O desenho e as fontes originais abaixo permanecem como referência.

## 1. Resultado, requisitos humanos e escolhas técnicas

### Requisitos já aprovados

- Substituir a timeline de fixtures do KLM Agent por ocorrências reais recebidas das conexões GitHub dos projetos. O pedido informal mencionou “Meta”, mas a integração pedida é **GitHub**; `meta` também é um tipo específico de webhook GitHub presente no screenshot.
- Contemplar **os 56 itens selecionáveis** do screenshot `C:\Users\Samuel\Downloads\gh.png`, inclusive bypass requests e dismissal requests. Não reduzir o catálogo ao enum antigo do Octokit ou à página padrão de documentação.
- Todos inicialmente habilitados. Botão de configuração do feed permite desmarcar tipos; isso altera **exibição**, nunca inscrição no GitHub, ingresso, liberação, retenção ou disponibilidade a outros consumidores. Tipos devem continuar reativáveis se o default mudar futuramente.
- Uma entrega distinta aceita/liberada representa uma atualização. A deduplicação existente continua sendo por conexão + delivery, não por PR, SHA, workflow ou similaridade de texto.
- Preservar o visual local aprovado: avatar/projeto + provider fixo **GitHub** + horário; ação com ícone/cor abaixo; frase factual; retângulo com título real; linha `base ← head · PR #98`, branch + run, ou referência adequada sem branch. O retângulo inteiro abre o recurso quando existe URL segura.
- Details expansível com descrição/dados úteis; bookmark continua sendo interação local. Sem supor autoria de agente a partir de username, bot ou nome do projeto.
- **Happening now já é real** e continua intacto, assim como chat, overlays, drafts, divisores/swap, proporções, abas compactas e navegação. Posts internos de agentes/Needs attention são etapa posterior.
- Ferramenta standalone para simular recebimentos assinados, com cobertura de todos os itens do screenshot e várias ações dos principais.
- Sem automação, execução de agente por evento, Tasks legadas, broker/banco, OAuth/PAT, registro remoto de hooks, commit/push, suítes ou revisão adversarial.

### Decisões técnicas deste plano, não atribuídas à aprovação humana

1. Feed como **projeção de leitura dos releases retidos**, com API global paginada descendente. Sem segunda coleção durável de posts, worker de feed, ACK ou cursor de consumidor persistido.
2. Normalização e filtragem no backend, retornando cards compactos. Details tem leitura compacta sob demanda. A UI não baixa payload bruto de até 25 MiB nem faz um request por card.
3. Configuração de exibição persistida **por cliente/origem e ENGINE_URL**, em localStorage versionado; vale para todos os projetos desse engine naquele cliente. Não inventar conta de usuário ou ProjectID para o general chat. Sincronização entre dispositivos não é parte desta primeira versão.
4. Bookmarks ficam em memória no feed, por eventId estável, preservados em refresh/filtros durante a montagem. Não criar storage/backend de bookmarks nem prometer preservação após reload; TTL continua prevalecendo.
5. Atualização visível por polling de 15 segundos + retorno ao foco/visibilidade + Retry/Refresh. Não depender de SSE para recuperar histórico. Reaproveitar mudanças de inventário apenas para acordar reconsulta de projetos; nenhum novo stream necessário.
6. Categorias: `PRs`, `Pipelines`, `Issues`, `Security`, `Repository`, `Other`, além de `All`. A categoria mock `Agents` sai da timeline real enquanto não há essa fonte; Happening now permanece.

## 2. Estado atual lido e preservação

| Arquivo | Fato relevante / orientação |
| --- | --- |
| `AGENTS.md`, `product.md` | Produto é interface de harnesses; validação leve, implementação mínima, UI em inglês. As descrições de timeline mock serão atualizadas somente depois da implementação. |
| `clients/desktop/src/features/klm-agent/KlmAgentWorkspace.tsx` | `ActivityCard` e `ActivityFeed` ainda usam `mockActivity`, índice de projeto e `https://example.com`. Alterações locais já moveram ação abaixo do byline, título para o retângulo e branches para `base ← head`. Partir desse arquivo de trabalho, não de HEAD. |
| `clients/desktop/src/features/klm-agent/klm-agent.css` | Estilos locais aprovados de `.activity-event`, `.activity-reference`, `.activity-reference-meta` e provider. Manter dimensões, cores/tokens, hierarquia e responsividade; adicionar apenas estados/configuração/paginação necessários. |
| `clients/desktop/src/features/klm-agent/mockActivity.ts` | Fixture visual aprovada, com body factual e baseBranch. Preservar arquivo como referência sem importá-lo no caminho real. Não transformá-lo em fallback quando API falhar. |
| `clients/desktop/src/App.tsx` | Passa projetos/sessões globais ao workspace; inventário SSE atualiza projetos/sessões. Não usar o ProjectID da sessão geral para consultar feed. Overlay e assinaturas de chats devem continuar iguais. |
| `clients/desktop/src/engine.ts`, `platform.ts` | Reusar `request`, `ENGINE_URL`, AbortSignal e resolução de base/prefixo/porta. Nenhuma URL localhost nova na UI. |
| `features/webhooks/api.ts` | CRUD, DTOs de metadata/releases e leitura raw já existem; não baixar raw para cada card. |
| `features/projects/ProjectSettingsDialog.tsx`, design-system | Padrão de `<dialog>`, restauração de foco, Button/IconButton/Input/Toggle; reutilizar visual/primitivas para Feed settings. |
| `engine/webhooks.go` | Estado imutável de evento/payload string, release separado, DTOs string para sequências, helpers de TTL/cursor. |
| `engine/webhooks_api.go` | HMAC obrigatório e dedupe durável. O header correto é **`X-Hub-Signature-256`**, não `X-GitHub-Signature-256`. |
| `engine/webhooks_worker.go` | Libera pending em batches de 100 e remove expirados; não é consumidor de feed. Não alterar seu significado. |
| `engine/api.go`, `main.go`, `store.go`, `updates.go` | Novas rotas de leitura e, se necessário, cache derivado no app. Não há necessidade de novos campos em diskState ou alteração de journal. |
| `engine/cli.go`, `runtime_dev.go`, `engine/README.md` | `KLM_DEV_DATA_DIR` muda dados/locks/control; **apiPort é const 17331**. Não existe flag/env de porta atualmente. Ver smoke isolado na seção 9. |
| `GITHUB_WEBHOOKS_IMPLEMENTATION_PLAN.md`, `engine/README.md` | Contrato existente de ingresso, releases, quotas, TTL e ciclo de vida é a base. A exclusão anterior de feed correspondia à etapa passada, não impede esta etapa autorizada. |

No início desta pesquisa, `git status --short` tinha somente os três arquivos klm-agent acima modificados. Não resetar, sobrescrever a partir de HEAD ou formatar esses arquivos inteiros. Este planejamento criou somente este documento. A implementação fará patches locais sobre o visual aprovado.

### Invariantes da base de webhooks

- Nova entrega: persistir evento + pending antes de 202; duplicata retida retorna 200 com mesmo ID e sem renovar TTL/horários/sequências.
- ReleaseSequence global é `uint64` Go e **string decimal em todo JSON/TS**. Nunca Number/parseInt/+ para comparar cursores; usar comparação decimal por comprimento/lexicográfica após normalização, ou BigInt transitório sem JSON de BigInt.
- `GET /api/projects/{id}/webhook-events` continua ascendente e exclusivo por `after`, independente de feed. Não mudar o contrato para atender a timeline.
- Só releases retidos, não expirados e de projetos ativos entram no feed. Pending não entra. Disabled continua permitindo leitura de entregas anteriores. Removed esconde, preserva dados/TTL; restauração torna releases retidos visíveis novamente.
- Excluir conexão/expirar remove fonte e projeção. Não guardar cópia de conteúdo no browser/localStorage, nem reter payload em cache após expiração. Bookmark não prolonga retenção.
- `ping` continua verificação/controle; fica fora da timeline e do catálogo configurável.
- Limites atuais permanecem: 10.000 eventos/256 MiB raw por engine, 25 MiB por body, dois ingressos lendo simultaneamente. Ler não consome dados de outros consumidores.

## 3. Catálogo técnico do screenshot e fontes

O screenshot contém **28 pares / 56 opções**. A tabela abaixo é a checklist de implementação e de fixtures. `—` em ação significa evento sem action obrigatória; estado pode estar em outro campo. Exemplos de ação não formam allowlist do ingresso.

Fontes: **D** = documentação GitHub padrão [S1]; **O** = schemas/exemplos Octokit [S3]; **E** = dados oficiais GitHub Docs Enterprise Cloud [S2]. **P** = confirmação parcial especificada logo após a tabela. O prefixo `exemption_request` foi conferido na fonte oficial; não substituir por objetos imaginários `bypass_request`/`dismissal_request`.

| # | Opção no screenshot | `X-GitHub-Event` / identificação | Ação/variante representativa | Alvo e categoria | Fonte |
| --- | --- | --- | --- | --- | --- |
| 01 | Related issues | `issue_relates_to` | `relates_to_added`, `relates_to_removed` | `issue`, `related_issue?`; Issues | D |
| 02 | Branch or tag creation | `create` | —; `ref_type=branch/tag` | `ref`, repository; Repository | D/O |
| 03 | Branch or tag deletion | `delete` | —; `ref_type=branch/tag` | `ref`, repository; Repository | D/O |
| 04 | Branch protection configurations | `branch_protection_configuration` | `enabled`, `disabled` | repository; Repository | D/O |
| 05 | Branch protection rules | `branch_protection_rule` | `created`, `edited`, `deleted` | `rule`; Repository | D/O |
| 06 | Bypass requests for push rulesets | **ver P abaixo**; discriminator oficial `exemption_request.request_type=push_ruleset_bypass` | screenshot: created/cancelled/completed/response submitted/dismissed; não impor enum não confirmado | `exemption_request`; Security | P/E |
| 07 | Bypass requests for secret scanning push protections | `bypass_request_secret_scanning` | `created`, `cancelled`, `completed`, `response_submitted`, `response_dismissed` | `exemption_request`, `exemption_response?`; Security | E |
| 08 | Check runs | `check_run` | `created`, `completed`, `rerequested`, `requested_action`; success/failure | `check_run`; Pipelines | D/O |
| 09 | Check suites | `check_suite` | `completed`, `requested`, `rerequested`; success/failure | `check_suite`; Pipelines | D/O |
| 10 | Code scanning alerts | `code_scanning_alert` | `created`, `fixed`, `appeared_in_branch`, `closed_by_user`, `reopened`, `reopened_by_user`, `updated_assignment` | `alert`; Security | D/O |
| 11 | Collaborator add, remove, or changed | `member` | `added`, `removed`, `edited` | `member`; Repository | D/O |
| 12 | Commit comments | `commit_comment` | `created` | `comment`; Repository | D/O |
| 13 | Dependabot alerts | `dependabot_alert` | `created`, `dismissed`, `fixed`, `reopened`, `auto_dismissed`, `auto_reopened`, `reintroduced`, `assignees_changed` | `alert`; Security | D/O |
| 14 | Deploy keys | `deploy_key` | `created`, `deleted` | `key.title/id` (não key material); Repository | D/O |
| 15 | Deployment statuses | `deployment_status` | `created` + `deployment_status.state` | deployment/status; Pipelines | D/O |
| 16 | Deployments | `deployment` | `created`; deleted do screenshot deve ser tolerado | `deployment`; Pipelines | D/O |
| 17 | Discussion comments | `discussion_comment` | `created`, `edited`, `deleted` | `comment`, `discussion`; Repository | D/O |
| 18 | Discussions | `discussion` | `created`, `answered`, `unanswered`, `closed`, `reopened`, demais ações documentadas | `discussion`; Repository | D/O |
| 19 | Dismissal requests for dependabot alerts | `dismissal_request_dependabot` | `created`, `cancelled`, `response_submitted` | `exemption_request`, `exemption_response?`; Security | E |
| 20 | Dismissal requests for code scanning alerts | `dismissal_request_code_scanning` | `created`, `response_submitted` | `exemption_request`, `exemption_response?`; Security | E |
| 21 | Dismissal requests for secret scanning alerts | `dismissal_request_secret_scanning` | `created`, `cancelled`, `completed`, `response_submitted`, `response_dismissed` | `exemption_request`, `exemption_response?`; Security | E |
| 22 | Forks | `fork` | — | `forkee`; Repository | D/O |
| 23 | Issue comments | `issue_comment` | `created`, `edited`, `deleted`, `pinned`, `unpinned` | `comment` + `issue`; PRs se `issue.pull_request` existe, senão Issues | D/O |
| 24 | Issue dependencies | `issue_dependencies` | `blocked_by_added`, `blocked_by_removed`, `blocking_added`, `blocking_removed` | `blocked_issue`, `blocking_issue`; Issues | D |
| 25 | Issues | `issues` | `opened`, `assigned`, `closed`, `reopened`, `edited`, e demais ações de D | `issue`; Issues | D/O |
| 26 | Labels | `label` | `created`, `edited`, `deleted`, `archived`, `unarchived` | `label`; Issues | D/O |
| 27 | Merge groups | `merge_group` | `checks_requested`, `destroyed` | `merge_group`; PRs | D/O |
| 28 | Meta | `meta` | `deleted` | `hook_id` (não config/secret do hook); Repository | D/O |
| 29 | Milestones | `milestone` | `created`, `opened`, `closed`, `edited`, `deleted` | `milestone`; Issues | D/O |
| 30 | Packages | `package` | `published`, `updated` | `package`; Repository | D/O |
| 31 | Page builds | `page_build` | —; `build.status` | `build`; Pipelines | D/O |
| 32 | Pull request review comments | `pull_request_review_comment` | `created`, `edited`, `deleted` | `comment`, `pull_request`; PRs | D/O |
| 33 | Pull request review threads | `pull_request_review_thread` | `resolved`, `unresolved` | `thread`, `pull_request`; PRs | D/O |
| 34 | Pull request reviews | `pull_request_review` | `submitted`, `edited`, `dismissed`; approved/changes_requested/commented | `review`, `pull_request`; PRs | D/O |
| 35 | Pull requests | `pull_request` | `opened`, `closed` merged/unmerged, `ready_for_review`, `synchronize`, demais ações de D | `pull_request`; PRs | D/O |
| 36 | Pushes | `push` | —; normal/created/deleted/forced, branch/tag | `ref`, `compare`, `head_commit?`; Repository | D/O |
| 37 | Registry packages | `registry_package` | `published`, `updated` | `registry_package`; Repository | D/O |
| 38 | Releases | `release` | `published`, `created`, `edited`, `deleted`, `prereleased`, `released`, `unpublished` | `release`; Repository | D/O |
| 39 | Repositories | `repository` | `created`, `deleted`, `archived`, `unarchived`, `publicized`, `privatized`, `edited`, `renamed`, `transferred` | repository; Repository | D/O |
| 40 | Repository advisories | `repository_advisory` | `published`, `reported` | `repository_advisory`; Security | D |
| 41 | Repository imports | `repository_import` | —; `status` | repository + status; Repository | D/O |
| 42 | Repository rulesets | `repository_ruleset` | `created`, `edited`, `deleted` | `repository_ruleset`; Repository | D |
| 43 | Repository vulnerability alerts | `repository_vulnerability_alert` | **`create`, `dismiss`, `reopen`, `resolve`** | `alert`; Security | D/O |
| 44 | Secret scanning alert locations | `secret_scanning_alert_location` | `created` | `alert`, `location`; Security | D/O |
| 45 | Secret scanning alerts | `secret_scanning_alert` | `created`, `resolved`, `reopened`, `validated`, `publicly_leaked`, `assigned`, `unassigned`; tolerar `revoked` do screenshot | `alert`; Security | D/O |
| 46 | Secret scanning scans | `secret_scanning_scan` | —; conclusão em `completed_at` | `type`, `source`; Security | D |
| 47 | Security and analyses | `security_and_analysis` | — | repository + `changes`; Security | D |
| 48 | Stars | `star` | `created`, `deleted` | repository; Repository | D/O |
| 49 | Statuses | `status` | —; `state=pending/success/failure/error` | `context`, `sha`, `commit`, `target_url?`; Pipelines | D/O |
| 50 | Sub issues | `sub_issues` | `sub_issue_added`, `sub_issue_removed`, `parent_issue_added`, `parent_issue_removed` | `sub_issue`, `parent_issue?`; Issues | D |
| 51 | Team adds | `team_add` | — | `team`; Repository | D/O |
| 52 | Visibility changes | `public` | — | repository; Repository | D/O |
| 53 | Watches | `watch` | `started` | repository; Repository | D/O |
| 54 | Wiki | `gollum` | —; ações por `pages[]` | pages (um card por entrega); Repository | D/O |
| 55 | Workflow jobs | `workflow_job` | `queued`, `waiting`, `in_progress`, `completed`; success/failure/cancelled/skipped | `workflow_job`; Pipelines | D/O |
| 56 | Workflow runs | `workflow_run` | `requested`, `in_progress`, `completed`; success/failure/cancelled/etc. | `workflow_run`, `workflow?`; Pipelines | D/O |

### P — limite concreto de confirmação: bypass de push rulesets

O item existe no screenshot, mas **não foi encontrado um nome canônico de header em fonte oficial consultada**. O repositório público `github/docs` confirma o discriminator `push_ruleset_bypass` e o modelo compartilhado de `exemption_request` nos `*.child-params.json` de E. A busca encontrou `bypass_request_push_ruleset` somente em código de terceiro, que também usa nomes de campos conflitantes com E; não tratá-lo como contrato GitHub conferido.

Implementar a opção com ID interno explícito **`push_ruleset_bypass`** (ID de catálogo, não alegação de header). Resolver primeiro os headers canônicos conhecidos; para um header desconhecido cujo objeto `exemption_request.request_type` seja exatamente `push_ruleset_bypass`, classificar nessa opção. Preservar sempre `eventType` original. Extrair somente os campos compartilhados documentados: number, requester_login, status, requester_comment, resource_identifier, html_url e, quando presente, exemption_response.status/reviewer_login. Isso permite receber, filtrar e apresentar essa família sem adivinhar o nome do header ou exigir payload inventado.

Para certificar esse header no futuro, basta ler o `value` do checkbox correspondente no HTML da página autenticada de criação do hook, ou uma entrega real/sanitizada do GitHub. Não criar/editar webhook remoto só para investigar. Registrar a fonte então e acrescentar o matcher exato sem trocar o ID da preferência. **Não bloquear os outros 55 itens**, nem declarar cobertura canônica de 56 headers enquanto essa evidência faltar.

Fixture do item 06: enquanto faltar evidência, usar header explicitamente sintético `klm_simulated_push_ruleset_bypass` e discriminator oficial, com a limitação indicada em `--list`/README/manifesto. Isso simula a projeção tolerante dessa opção, **não certifica um delivery real desse tipo**. Aceitar `--event`/payload externo permite substituir pelo exemplar real sem mudar o simulador.

### Outros cuidados do catálogo

- IDs de catálogo dos outros 55 itens são seus nomes canônicos. A versão inicial contém 56 opções, `defaultEnabled:true` em todas. `ping` não entra; tipos fora do screenshot continuam tolerados, em Other, sem nova opção automática.
- Ações desconhecidas de tipo conhecido **mantêm a mesma opção de configuração**. Não exigir lista fechada para normalizar; usar título factual genérico com action literal/humanizada.
- `registry_package` e `repository_vulnerability_alert` são antigos/depreciados, mas permanecem porque aparecem no screenshot. Não fundir sua configuração com package/dependabot.
- `watch` e `star` são entradas distintas. Screenshot/Octokit descrevem watch como star; a página D consultada descreve watching. Usar rótulo conservador “Watch event”/action “started”, sem inventar significado adicional.
- As disponibilidades variam por repository/org/app e plano GitHub. Por exemplo, D restringe certas ações de check_run/check_suite a Apps e lista merge_group com disponibilidade divergente do screenshot. Catálogo UI não é promessa de que toda ação será entregue a todo hook.
- Uma entrega com vários commits/pages/alerts continua um card com detalhes/lista limitada; duas conexões recebendo o mesmo fato têm ocorrências próprias. Não colapsar duas entregas por recurso.

## 4. APIs novas: leitura global, compacta e paginada

Escolha de nomes fechada para o implementador; não alterar APIs existentes.

### 4.1 Catálogo

`GET /api/activity/github/catalog`

```ts
type ActivityCategory = 'PRs' | 'Pipelines' | 'Issues' | 'Security' | 'Repository' | 'Other';
type ActivityCatalog = {
  version: 1;
  items: { id: string; label: string; defaultEnabled: boolean; category: ActivityCategory }[];
};
```

Catálogo declarativo em Go é fonte de verdade para labels/IDs/classificação/normalizador. UI obtém as opções dessa API, sem manter outra lista manual de 56 tipos. Não baixar catálogo remoto durante execução do produto. Simulador tem manifesto de fixtures cuja cobertura por catalogId pode ser conferida por `--list`/dry-run; nenhuma dependência runtime do npm Octokit necessária.

### 4.2 Página do feed

`GET /api/activity/github?limit=50&cursor=...&category=PRs&q=...&exclude=issues&exclude=push`

- `limit` default 50, 1–100; `category` ausente/All = todas; enum inválido dá 400.
- `q` opcional, trim, máximo 200 caracteres; busca case-insensitive por substring nos **campos exibíveis de resumo**: projeto/repositório/ator/título/frase/referência/branches/ação/tipo/provider. Não prometer full-text de body bruto. Aplicar no backend **antes da paginação**.
- `exclude` repetível contém IDs de catálogo desabilitados. Deduplicar/ordenar, limitar a 56 na versão atual, rejeitar desconhecidos com 400. Preferências locais de versões futuras devem ser intersectadas com catálogo carregado antes da chamada.
- Unknown real que não se enquadre num catalogId fica visível por default em All/Other; desmarcar todos os tipos conhecidos não perde esses eventos silenciosamente. Settings configura só catálogo do screenshot.
- Sem projeto selecionado no request: a consulta inclui todos os `Project.Removed=false` com conexão/release válidos. Ownership deriva do storage, jamais de `repository` do payload. O endpoint não cria general session nem projeto sintético.

```ts
type GithubActivityItem = {
  id: string; // eventId; chave estável React
  projectId: string; projectName: string; webhookId: string;
  provider: 'github';
  eventType: string; action?: string; catalogId?: string;
  releaseSequence: string; receivedAt: string; emittedAt: string; expiresAt?: string;
  category: ActivityCategory;
  tone: 'accent' | 'success' | 'danger' | 'warning' | 'muted';
  icon: string; // pequeno enum local de ícones, não SVG/HTML arbitrário
  actionLabel: string; summary: string;
  actor?: string; repository?: string;
  title: string; reference: string;
  branch?: string; baseBranch?: string;
  targetUrl?: string; targetKind?: 'resource' | 'repository';
  result?: string; // Success, Failure, Cancelled etc.; só quando conhecido
  summaryTruncated?: boolean;
};
type GithubActivityPage = {
  items: GithubActivityItem[];
  nextCursor?: string; hasMore: boolean;
  highWatermark: string;
  asOf: string; // relógio UTC do engine para expiração/horário relativo
};
```

Todos opcionais são omitidos; arrays vazios `[]`. projectName vem do inventário capturado, não do payload; ícone é resolvido pelo Map de projetos existente na UI, com iniciais se não disponível. Não duplicar imagens base64 em cada card nem usar fallbackProjects mock.

#### Cursor e ordenação

1. Na primeira página, sob `app.mu`, capturar contador global `highWatermark` e metadata/referências de strings imutáveis necessárias. Selecionar releases cujos eventos/conexões/projetos sejam válidos, não ping e não expirados no instante da consulta.
2. Ordenar por **releaseSequence decrescente**. Card mostra `receivedAt` como horário de recebimento; não inventar data de ocorrência GitHub. Uma liberação atrasada pode aparecer no topo com recebimento mais antigo, deliberadamente.
3. Cursor base64url versionado contém `v`, `kind=github-activity`, `high` decimal string, `before` decimal string exclusiva e hash do filtro canônico (`q`, `category`, exclude ordenado). Máximo 1 KiB, decoder estrito. Validar uint64, versão/kind, `0 < before <= high <= contador atual`; cursor de outro filtro dá 400.
4. Páginas seguintes usam `seq <= high && seq < before`. Normalizar/filtrar e obter **limit + 1 resultados que de fato correspondem**, não limitar primeiro e filtrar depois. `hasMore` refere-se a resultados elegíveis, não ao número bruto de releases. Cursor usa a sequência do último item devolvido.
5. Lacunas por TTL/delete/ping/filtros são normais; cursor não exige que o evento de fronteira exista. Novos releases acima de high entram no próximo refresh, não no meio da paginação antiga. Fim real: hasMore=false/nextCursor ausente.
6. Snapshot não congela retenção/visibilidade para sempre: cada página reavalia TTL e projetos ativos. Exclusão concorrente pode terminar uma leitura já iniciada com snapshot anterior, como na API atual. Nova consulta deve excluir o registro.
7. Não persistir cursor no cliente. Após reload/restart/reconexão prolongada, partir da primeira página. Erro de cursor por substituição/restauração do banco inicia reload explícito, sem apresentar resposta vazia falsa.

#### Custo e cache

- Toda extração de payload ocorre **fora de app.mu**, usando a string imutável capturada. Não prender o lock enquanto decodifica 25 MiB ou escreve HTTP.
- Para buscas poderem varrer até 10.000 registros sem reparsing contínuo, usar memoização **volátil e limitada** de resumo por eventId + versão do normalizador. Nada em diskState/journal, nenhuma cópia de raw/body completo. Limite por entradas e bytes (por exemplo 10.000/64 MiB), eviction de cache não apaga evento.
- Separar cache de resumo do projectName atual, para renomear projeto não produzir busca/nome velho. Revalidar acesso/TTL a cada query e podar caches para IDs não mais retidos/expirados; cache nunca autoriza exibir item.
- Resumo máximo ~8 KiB serializados/item, com limites por campo (título/summary até 1 KiB cada, refs/ator/branches limitados, URL até 2 KiB, sem body). Não truncar URL para transformá-la em outro destino: rejeitar URL excessiva. Incluir indicador de truncamento se necessário.
- Decodificar de forma tolerante com números preservados (`json.Decoder.UseNumber` ou structs/json.Number/RawMessage adequados). Não converter IDs GitHub a float64 para compor referências. Limitar arrays e saída; falha de um campo não deve apagar o card. Erro inesperado da consulta é erro HTTP, não `items:[]`.
- Não buscar enriquecimento na API GitHub, resolver URLs por request, baixar avatares remotos do sender ou executar qualquer conteúdo do payload.

### 4.3 Details compacto

`GET /api/activity/github/{eventId}`

Verificar ID, release, conexão, projeto ativo, não ping e TTL antes de responder; 404 se indisponível. Reusar mesmo normalizador/segurança da listagem.

```ts
type GithubActivityDetails = {
  id: string;
  fields: { label: string; value: string }[];
  body?: string; // texto literal, não HTML
  entries?: { text: string; url?: string }[];
  truncated: boolean;
};
```

- Campos explícitos por família: estado/action, autor/sender quando disponível, repo, número, branches, PR draft/ready/changed_files, review state, conclusão CI, duração apenas com timestamps válidos, assignee/labels, IDs úteis. Limitar fields (ex. 20), entries (ex. 20) e body (ex. 32 KiB), com teto de resposta 64 KiB e `truncated:true` quando necessário.
- Body de PR/issue/discussion/release/review/comment preservado dentro desse limite; erro/payload parcial conserva metadata útil. Truncamento deve aparecer discretamente em Details (`Details truncated. Open on GitHub.` quando há link).
- **Não** devolver o payload bruto, dump JSON, `alert.secret`, deploy key material, `hook.config` ou secret/assinatura da conexão. Raw API existente permanece independente para consumidores que realmente precisem dela.
- Chamar apenas ao expandir um card; reutilizar durante a montagem enquanto retido; cancelar/ignorar resposta tardia após desmontar ou expirar. Loading/Retry no próprio Details. 404 remove/atualiza card e informa indisponibilidade brevemente, não derruba todo feed.

## 5. Normalização e apresentação factual

Implementar helpers puros em Go, separados de HTTP/armazenamento, com mapa de famílias e extração segura por caminhos documentados. A API fornece os textos em inglês; UI mapeia icon/tone para Lucide/tokens existentes. Não gerar SVG/CSS/texto executável do payload.

### Regras principais

- PR: `pull_request.title`, `.html_url`, `.number` ou top-level number; `.base.ref ← .head.ref`. `action=closed && merged===true` = **Pull request merged**; `merged===false` = closed; merged ausente = closed, nunca afirmar merge. opened usa sender para ação quando identificado; não confundir `.user.login` (autor da PR) com quem fez qualquer ação posterior. Details inclui draft/ready só se boolean conhecido e `changed_files` só se recebido.
- Issue comment: `issue.pull_request` distingue conversa de PR de issue. Preferir `comment.html_url` para retângulo; título da issue/PR; Details com comment.body. Não inventar branches ausentes.
- Reviews: título/branches da PR, review.html_url ou comment.html_url; thread usa URL de comentário conhecido quando disponível ou PR. Separar submitted do review.state (`approved`, `changes_requested`, `commented`), sem equivaler todo review a aprovação.
- CI: completed **não** é success. Ler conclusion/state exato; null/unknown = completed/status sem afirmação de sucesso. success verde; failure/timed_out/error vermelho; cancelled/skipped/neutral/stale não “Passed”; queued/waiting/in_progress têm status factual. Sem porcentagem fake (o 68% do mock não é dado real). `workflow_run.run_number` para Run #; workflow_job pode usar Job #/Run ID se só IDs estiverem presentes, sem chamar run_id de run_number.
- Push: ref heads/tags identificada corretamente, SHA resumido, head_commit.message/compare quando disponíveis, count somente da informação presente (array pode ser parcial). `deleted=true` ou head_commit=null não quebra. Details lista bounded commits; não criar um post por commit.
- Relações de issue: usar os nomes reais `blocked_issue`/`blocking_issue`, `parent_issue`/`sub_issue`, `issue`/`related_issue?`. Cross-repo relates_to pode omitir totalmente o alvo relacionado; não sintetizar uma issue que não veio.
- Security/bypass/dismissal: resumo de ação sobre alerta/solicitação, tipo/estado e número; nunca conteúdo de secret. `exemption_response.status` diferencia aprovação/rejeição quando veio; `response_submitted` sozinho não significa approved. Nada desses eventos vira request de permissão KLM ou inicia agente.
- Repo/admin/social: descrição curta da ação, nome do alvo/documentado, repo e sender quando disponível. `meta.deleted` é informação sobre hook GitHub, **não** instrução para excluir a conexão KLM. `repository.deleted` não remove projeto KLM.
- Unknown: `GitHub event received` + eventType/action factual, repo/sender/alvo conhecido quando existentes. Fallback neutro; não descartar payload parcial/nulo por não satisfazer um schema completo. Não adivinhar objeto específico por campos soltos incompatíveis com o tipo.
- Provider visível sempre **GitHub**, mesmo sender com sufixo `[bot]`, ghost ou nomes como Implementation agent. Sender ausente/ghost pode usar frase passiva sem “by”. Nunca inserir “KLM Agent published…” por inferência.

### URLs

- Aceitar apenas URL absoluta com `http:`/`https:`, hostname não vazio, sem userinfo/controles. Mesma validação backend e defesa frontend; `target="_blank" rel="noopener noreferrer"`. Não aceitar javascript/data/file/protocol-relative nem `href="#"` substituto.
- Precedência por tipo: PR/issue/discussion/review/comment/release/alert/advisory/workflow/job/forkee `html_url`; push `compare`/head_commit.url; status `target_url` seguro/commit.html_url; solicitações `exemption_request.html_url`; wiki pages[].html_url; ruleset `_links.html.href` se presente no formato documentado; depois repository.html_url.
- Recurso sem URL de página pode cair no repositório com `targetKind=repository` e aria-label `Open repository…`. Não chamar endpoint REST `.url` indiscriminadamente de página do recurso; não compor rotas imaginárias de security/admin. Se ambos faltam, renderizar mesmo retângulo como elemento não clicável.
- Título e referência devem continuar factuais quando o link é só do repo. Recurso deletado pode naturalmente abrir 404 no GitHub; não fazer probe de rede para escondê-lo.
- Renderizar texto com escape React e `white-space: pre-wrap` no Details existente. Texto puro é a opção mínima; não introduzir renderer HTML/raw Markdown. Se reutilizar Markdown futuramente, sanitização e restrição de URLs continuam obrigatórias.

## 6. Frontend: estado, atualização, filtros e retenção

Criar `features/klm-agent/githubActivityApi.ts`, `useGithubActivityFeed.ts`, `ActivityFeedSettings.tsx` e, se simplificar o arquivo atual, componente de card real separado. Mantém `WorkspaceDivider`, `HappeningNow` e wrapper de chat existentes.

### Carregamento e navegação

1. Ao montar o feed, carregar catálogo e preferências; só consultar itens com filtros resolvidos. Mostrar loading real. Zero projetos/eventos é vazio real, não exemplos de KLM/Cubiq/Workspace.
2. Primeira página vem newest-first. `Load more` usa cursor retornado; dedupe por eventId em qualquer merge defensivo. Só mostrar fim (`No more updates`) quando hasMore=false, nunca “caught up” após só uma página.
3. Busca debounce ~250 ms; categoria/exclusões mudam query inteira, cancelam requests e resetam para primeira página. Usar geração da query + AbortController para impedir que resultados velhos substituam busca/configuração nova.
4. Filtros são aplicados no backend a todo histórico elegível. Não limitar filtro à página carregada, nem retornar “No matching updates” enquanto há páginas ainda não pesquisadas. Scroll horizontal/overflow das tabs preserva categorias em viewport estreito.
5. Erro inicial: mensagem + Retry. Erro de próxima página: mantém cards e cursor anterior + Retry. Erro de refresh: mantém último snapshot com aviso recuperável de atualização, não estado vazio. Catálogo/prefs com erro nunca caem em defaults silenciosos que ocultem preferências conhecidas.

### Refresh sem buracos de paginação

Implementação mínima escolhida: **reconsultar a janela carregada, com swap atômico**, em vez de combinar um topo novo com um cursor antigo e pular eventos.

- Guardar em memória o limite inferior (releaseSequence do último card carregado) e a posição/ID da âncora visível. Em refresh, consultar primeira página com novo high e seguir nextCursor **até alcançar/cruzar esse limite inferior ou esgotar resultados**. Primeira janela sem itens: só primeira página.
- Montar resultado temporário de páginas contíguas do novo snapshot. Só trocar lista/cursor/hasMore após a janela inteira carregar. Se falhar qualquer página, preservar janela anterior e mostrar erro. Não truncar silenciosamente refresh para N páginas fixas quando chegaram mais que N páginas novas.
- Pode incluir a página completa que cruza a fronteira; isso é seguro e deixa próximo cursor correto. Se a linha de fronteira expirou/excluiu, a comparação numérica string ainda funciona; não esperar encontrar aquele ID para sempre.
- Serializar Load more e refresh; um refresh nunca sobrescreve paginação aceita depois dele. Coalescer wakeups durante request, não disparar requests paralelos por cada atualização de sessão.
- Manter âncora/scroll quando usuário lê histórico e IDs ainda existem; não forçar scroll ao topo. Expand/collapse e bookmarks não devem resetar por refresh porque card usa key eventId e estado por ID.
- Poll de 15s apenas com Activity visível + documento visível; na tela compacta Chat, pausar. Return ao foco/Activity dispara refresh imediato. Não criar timer global que funcione quando KLM Agent está fechado. Abort na desmontagem. Sem persistência de cursor necessário para restart/reload.
- Usar fingerprint estável de projetos (IDs/nome/ícone) para mudanças reais de inventário; não depender da identidade de arrays que mudam por tokens de chat. Projeto retirado do inventário deve ser removido da janela de imediato e disparar refresh; restauração também precisa refresh completo para releases antigos reaparecerem.
- TTL: usar `asOf` para offset do relógio e `expiresAt` por card; esconder cards vencidos enquanto aberto, mesmo antes da limpeza do worker. Reconsulta limpa exclusão de conexão e qualquer diferença de lifecycle em até próximo poll/foco. Não confiar só em highWatermark: exclusão/TTL/restauração não necessariamente o incrementam.
- Settings e filtros não afetam retenção. Desmarcar/reativar recupera tudo que ainda estiver retido, incluindo histórico recebido enquanto desmarcado.

### Preferências e bookmarks

- Chave: `klm.agent.github-feed.preferences.v1:${encodeURIComponent(ENGINE_URL)}`. Não usar ProjectID, ID do chat geral ou segredo. Base atual de engine é suficiente para escopo local mínimo; mudança de URL pode criar preferências separadas, documentar.
- Valor: `{version:1, overrides: Record<catalogId, boolean>}`. Ausente = default do catálogo. Overrides explícitos prevalecem sobre futuros defaults; preservar entradas de versões futuras em storage, mas enviar somente IDs do catálogo corrente. Validar JSON/tipos, não aceitar prototypes/campos arbitrários como configuração.
- Botão **Feed settings** na área do cabeçalho, dialog compacto com pesquisa de tipos, lista rolável das 56 opções, checkboxes/toggles acessíveis e **Enable all**. Close/Escape restaura foco. Não adicionar configuração de recebimento/secret aqui.
- Ao mudar, persistir localmente e atualizar query. Se localStorage falhar, manter seleção da sessão e indicar `Preferences could not be saved.`; não alegar durabilidade. Evento storage sincroniza abas da mesma origem/engine quando houver.
- Bookmark: Set de IDs em memória do ActivityFeed, não boolean que reinicia em remount por filtro. Ao apagar/expirar, card some; bookmark não conserva conteúdo nem gera post fantasma. Não adicionar filtro de bookmarks/persistência como escopo extra.

## 7. Simulador standalone e fixtures

### Artefatos

- `scripts/github-webhooks/simulate.mjs`: Node standalone, somente módulos nativos (`crypto`, `fs`, `http/https` ou fetch), sem dependências/servidor embutido.
- `scripts/github-webhooks/fixtures.mjs` ou subdiretório com JSON + manifesto: geradores sintéticos por família/ação, nomes de cenários estáveis e `catalogId` + fonte/limitação. Compartilhar envelope repository/sender sem exigir esses campos no ingresso.
- `scripts/github-webhooks/README.md`: setup, comandos, cobertura, limitações, respostas esperadas. Não guardar secrets/credenciais nem dumps reais privados no repo.

### CLI obrigatória

```text
node scripts/github-webhooks/simulate.mjs --list
node scripts/github-webhooks/simulate.mjs --all --dry-run
node scripts/github-webhooks/simulate.mjs --fixture pr-opened --url <endpoint> --secret <secret>
node scripts/github-webhooks/simulate.mjs --all --url <endpoint>
node scripts/github-webhooks/simulate.mjs --fixture workflow-failure --repeat 3
node scripts/github-webhooks/simulate.mjs --fixture pr-opened --repeat 2 --same-delivery
node scripts/github-webhooks/simulate.mjs --fixture pr-opened --invalid-signature
node scripts/github-webhooks/simulate.mjs --payload <path.json> --event pull_request
```

- URL/secret por `--url`/`--secret` ou `KLM_GITHUB_WEBHOOK_URL`/`KLM_GITHUB_WEBHOOK_SECRET`; CLI prevalece. Preferir env para secret nos exemplos PowerShell. Nunca imprimir secret/signature, inclusive no dry-run/erro; não registrar request bruto de payload externo. Evitar eco de URL com userinfo/query secreto; aceitar endpoint http/https sem userinfo e não seguir redirects de POST com segredo.
- `--list`: cenários, event header, catalogId, action/estado e indicação sintético/parcial. `--dry-run` não usa rede, não exige secret/URL e mostra cenário/metadata/bytes/limitação; valida cobertura dos 56 catalogIds e JSON dos fixtures antes de enviar qualquer `--all`.
- Sem flag de cenário/arquivo: mostrar help, nunca enviar todos implicitamente. `--all` é envio explícito sequencial, sem avalanche concorrente. `--repeat` inteiro positivo bounded (ex. 1–1000), delay opcional, sem retries automáticos.
- Gerar delivery UUID novo por ocorrência por default. `--delivery-id` permite valor explícito. `--same-delivery --repeat N` repete **mesmo event header e mesmo Buffer**; todos os metadados/bytes são congelados antes do primeiro envio. Não regenerar timestamp/body entre duplicatas. Rejeitar combinações ambíguas `--all --same-delivery` para não deduplicar cenários distintos.
- Assinar `HMAC-SHA256(secret, raw Buffer)` e transmitir exatamente esse Buffer: `Content-Type: application/json`, `X-GitHub-Event`, `X-GitHub-Delivery`, **`X-Hub-Signature-256: sha256=<hex>`**. Não usar form-urlencoded que aparece selecionado no screenshot; a base KLM aceita JSON.
- Payload externo: `readFile` como Buffer, validar UTF-8/objeto JSON sem reserializar, sem substituir placeholders/normalizar newline. Assinatura e transmissão preservam bytes incluindo whitespace; exigir `--event` explícito. Fixtures parametrizáveis (`--repo owner/name`, `--actor`, `--base`, `--head`, `--number`) são serializados **uma vez**, depois assinados.
- `--invalid-signature` altera um nibble do digest válido sem mudar body; esperar 401 e nenhum evento. CLI imprime HTTP status + accepted/duplicate/eventId quando presentes, sem payload/credenciais. Status inesperado dá exit code não zero; 200 duplicate no cenário específico é esperado.
- Nunca criar/remover projetos/conexões nem chamar GitHub real. O usuário/owner fornece endpoint e secret de dados sintéticos. `--all` envia eventos ao endpoint informado, não tem efeito de setup/destruição implícito.

### Cobertura mínima de cenários

1. **56 linhas da tabela:** pelo menos uma fixture sintética representativa por catalogId. O item 06 usa classificação estrutural e header de simulação claramente declarado até haver evidência canônica. Não apresentar `{action,repository,sender}` vazio como cobertura rica de todo tipo: incluir alvo documentado de cada família.
2. PR: opened com body/changed_files/base/head, ready_for_review, synchronize, closed merged=true, closed merged=false, reopened, ação futura desconhecida.
3. Issues e comentários: opened/assigned/closed, issue_comment em issue e em PR, comentário edited/deleted, relação issue cross-repo com alvo omitido; sub_issues e dependencies com nomes reais de objetos.
4. Reviews: approved/changes_requested/commented submitted, comment created, thread resolved/unresolved.
5. Workflow run/job: requested/queued/waiting/in_progress; completed success/failure/cancelled/skipped; completed sem conclusion. Checks: completed success/failure, created/requested; status pending/success/failure/error; deployment_status success/failure.
6. Push normal, forced, branch deleted com head_commit=null, tag; create/delete branch e tag. Wiki com múltiplas pages continua uma ocorrência.
7. Security: created/resolved ou fixed, solicitações created e response_submitted approved/rejected quando schema confirma, body que contenha campo secret para garantir que não vai para apresentação. Só valores sintéticos inofensivos.
8. Tolerância: tipo desconhecido, ausência de sender/repository, campos inesperados/nulos, URL inválida `javascript:`, body com HTML literal e Unicode. Sintéticos e documentados como verificação de fallback/escape, não corpus de revisão adversarial.
9. Controles adicionais: ping (aceito/verified, fora da timeline), same-delivery, invalid-signature. Mesmo recurso com novo delivery vira outra atualização; mesmo delivery em outra conexão também é independente.

Fixtures são exemplos **parciais sintéticos**, não certificação de todos os schemas ou de entrega externa GitHub. Distinguir variantes disponíveis somente para Apps da simulação genérica que o ingresso aceita. Não exigir suíte ou instalar validador gigante para rodar ferramenta.

## 8. Arquivos e sequência de implementação

### Arquivos previstos

| Arquivo | Mudança |
| --- | --- |
| Novo `engine/github_activity_catalog.go` | Catálogo 56, IDs e famílias, incluindo classificação estrutural explicitada. |
| Novo `engine/github_activity.go` | DTOs compactos, extração/normalização factual, URL/text bounds, helpers e cache derivado. |
| Novo `engine/github_activity_api.go` | Catálogo, consulta paginada global, filtros/cursor e Details. |
| `engine/api.go` | Registrar somente três rotas de leitura novas. |
| `engine/main.go` | Campos/inicialização lazy do cache no app, se necessário; sem novo worker. |
| Novos `features/klm-agent/githubActivityApi.ts`, `useGithubActivityFeed.ts`, `ActivityFeedSettings.tsx` | Cliente, refresh/paginação/erros/TTL, preferência e configuração. |
| `features/klm-agent/KlmAgentWorkspace.tsx` | Substituir fonte mock e adaptar card/feed à API, preservando estrutura local aprovada. Extração opcional de card para arquivo próprio, sem remodelar shell. |
| `features/klm-agent/klm-agent.css` | Adições pontuais para settings/loading/erro/load more/overflow de categorias. |
| `features/klm-agent/mockActivity.ts` | Preservar referência visual local; remover apenas import do caminho real, sem apagar trabalho do pai. |
| `scripts/github-webhooks/*` | CLI, fixtures/manifesto e README. |
| `product.md`, `engine/README.md` | Atualizar timeline real/APIs/retention/preferência local/simulador/limitações e status verdadeiro de validação. |

Sem mudanças previstas em store/journal/transactions, bridge, prompts, graph, Tasks, Flutter, Tauri ou settings de webhooks. Qualquer necessidade diferente deve ser justificada pelo implementador, não virar refactor amplo.

### Ordem executável

1. Reler working tree e seções 1–3; conferir que só há alterações esperadas ou preservar novas alterações concorrentes. Não resetar os três mocks aprovados. Reconhecer a pendência do header item 06 e implementar fallback estrutural, sem travar o restante.
2. Criar catálogo + DTOs + normalizador puro e limites; aplicar primeiro PR/issue/comment/CI, depois famílias genéricas até cobrir todos os 56 itens. Separar conhecidos/unknown e manter números/seq como strings.
3. Implementar consulta de releases retidos e Details, cursor/filtros/TTL/Removed e cache bounded. Registrar rotas. Não alterar ingresso/worker existente para “consumir” feed.
4. Criar simulador/fixtures em paralelo lógico ao normalizador (mesmo owner, sem nova delegação), explicitando proveniência e cobertura. Exercitar `--list` e `--all --dry-run` sem rede e sem secrets.
5. Implementar hook de feed, estado de erro/paginação/refresh e preferência. Integrar card sobre o markup/CSS local aprovado, provider GitHub fixo e link seguro. Remover mocks do render real, sem posts internos.
6. Adicionar Feed settings e categorias. Passar visibilidade compacta ao hook sem mudar callbacks/semântica de overlay/chat/Happening now. Evitar alterar App se projetos/sessões existentes bastarem.
7. Atualizar docs, executar checks leves abaixo uma vez depois de concluir cada linguagem; corrigir erros introduzidos e repetir só se correção exigir. Entregar à origem arquivos/contratos/checks efetivos, cenários não executados e a pendência canônica item 06.
8. Origem faz Playwright pontual do visual/integridade dos painéis após integração, sem duplicação pelo implementador. Não declarar comportamento runtime validado só porque Go/TS compilaram.

## 9. Checagem leve e smoke manual isolado

### Checks do owner de implementação

- Em `engine`: **`go build -tags dev ./...`**.
- Em `clients/desktop`: **`npx tsc -b`**.
- Simulador: `node scripts/github-webhooks/simulate.mjs --list` e `--all --dry-run` (o próprio carregamento verifica sintaxe dos módulos e cobertura das fixtures). Sem instalar dependências novas só para testar.
- Não executar `go test ./...`, suítes frontend, integração versionada ou agentes de review. Planejador não executa builds; neste trabalho só houve pesquisa e criação do documento.

### Armadilha real de isolamento

O engine desta conversa está em **17331**, frontend em **5173**. **Não parar/reiniciar** esses processos. `KLM_DEV_DATA_DIR` sozinho não permite um segundo engine: `runtime_dev.go` define `apiPort` como const.

Se o owner optar pelo breve smoke autorizado, a solução sem infraestrutura/alteração de produto é gerar **temporariamente fora do repo**, em `C:\Users\Samuel\AppData\Local\Temp\opencode`, um arquivo de overlay Go que substitua apenas `engine/runtime_dev.go` por conteúdo idêntico com apiPort livre (ex. 27331, após conferir disponibilidade). Compilar o executável de smoke com `go build -tags dev -overlay <overlay.json> -o <tmp>\klm-feed-smoke.exe .`. Overlay JSON segue `{"Replace":{"<caminho absoluto runtime_dev.go>":"<caminho absoluto runtime_dev_smoke.go>"}}`. Isso é compilação para smoke, não uma segunda rodada de suíte/check por rotina. Não modificar `runtime_dev.go` do workspace nem inventar `KLM_DEV_PORT` que não existe.

Usar `KLM_DEV_DATA_DIR=<diretório absoluto novo e descartável>` **em todos** start/stop desse executável, mantendo override no ambiente do processo filho. O control channel deriva do diretório; stop só do runtime descartável. Usar diretórios sintéticos vazios como projetos. Verificar diretório pai antes de criar arquivos; não copiar state/journal/secrets do engine real. Se não fizer smoke, registrar “não executado”, sem parar 17331 para contornar a limitação.

Frontend da origem: não apontar silenciosamente o browser de trabalho 5173 para o engine descartável. Para validação ponta a ponta, combinar com a origem uso de instância/origem Vite separada com `VITE_ENGINE_URL` do smoke (porta livre), ou deixar validação para quando ela puder disponibilizar o backend novo. A decisão de Playwright é da origem; nenhum processo desta sessão deve ser interrompido.

### Roteiro curto de smoke (não alegação de execução)

1. Criar manualmente via API dois projetos descartáveis e duas conexões, armazenando secret somente no ambiente local temporário. Não executar harness/session/graph.
2. Enviar PR opened, workflow success/failure, issue_comment PR/issue, um tipo raro, fallback estrutural do item 06 e unknown; depois same-delivery e invalid-signature. Conferir 202/200/401 e ausência de card para ping.
3. Consultar catálogo (56 opções), feed limit=2 até fim, Details compacto; conferir ordem string, um card por ocorrência, URLs, PR merged/unmerged, filtragem antes da paginação e buscas de item antigo. Enviar outra ocorrência durante paginação e confirmar que refresh a encontra sem lacuna.
4. Reiniciar **só** runtime descartável com mesmo DATA_DIR e conferir IDs/seq/horários/histórico mantidos, sem “consumo” por leituras repetidas. Novo cliente parte do topo e vê retidos.
5. UI pontual pela origem: configuração desmarca/reativa; reload mantém preferência local; refresh conserva Details/bookmark da montagem/scroll; erro backend aparece com Retry; link abre recurso seguro. Conferir chat/overlay/Happening now e ambos splitters sem regressão visível.

Demais cenários são checklist manual preparado, não expandir smoke para suíte:

- Mais de uma página de novas entregas durante feed aberto; refresh da janela carregada não pula o intervalo entre topo e histórico.
- Conexão disabled retém histórico; exclusão confirmada de conexão descartável remove cards; remover/restaurar projeto descartável oculta/recupera retidos sem mudar seq.
- TTL em consulta/detalhe, timer local e cache; usar dados descartáveis/relógio injetado em helper ad hoc se necessário, **não** endpoint público de relógio, engineTime legado ou editar journal em uso. Sem promessa de testar 1 ano de retenção neste smoke.
- Seq maiores que `Number.MAX_SAFE_INTEGER` no helper de cursor/comparação de forma local e descartável; não fabricar/corromper estado persistido para obter isso.
- Body grande produz resumo/detail bounded e truncamento visível, não 25 MiB por card; payload íntegro continua na API raw existente enquanto retido.
- Contagem da cobertura do simulador separada de cobertura de entrega externa: 56 opções sintéticas, 55 headers canônicos confirmados aqui, item 06 com ressalva até evidência real.

## 10. Critério de entrega e pendências

- [x] APIs globais implementadas; paginação/filtros exercitados no smoke, sem alterar API after existente.
- [x] Timeline ligada à API; smoke confirma projeção dos 56 IDs, sem ping; import do mock removido, arquivo visual preservado.
- [x] Settings local por engine/origem, overrides explícitos, Enable all, erros e sincronização storage implementados; seleção e persistência após reload exercitadas com Playwright.
- [x] Card real preserva classes/estrutura aprovada; provider GitHub, copy, Details/link seguro e layouts responsivos conferidos com Playwright. Chat/Happening now/divisores preservados; aceitação humana continua pendente.
- [x] Paginação/refresh atômico da janela, cancelamento, erros, TTL e inventário implementados. Paginação/restart API e refresh com 105 ocorrências novas/âncora de scroll exercitados; TTL decorrido continua pendente.
- [x] Simulador standalone: 56 IDs, 124 cenários, bytes exatos/HMAC, controles e README.
- [x] Go/TS/list/dry-run executados; smoke API isolado e Playwright pontual da origem executados.
- [x] Docs atualizadas com distinção entre implementação, smoke API e aceitação humana/externa.

**Bloqueadores de implementação geral:** nenhum; o desenho mínimo não depende de nova decisão humana. **Limitação específica de certificação:** header canônico do bypass de push rulesets não confirmado em fontes oficiais; a classificação estrutural documentada permite funcionalidade/seleção/simulação dessa família sem inventar contrato. A entrega deve conservar essa ressalva até haver checkbox value/entrega real verificável. O smoke também depende de executável temporário com porta livre (ou fica pendente), porque alterar DATA_DIR não muda a porta existente.

## 11. Fontes consultadas e alcance

- **S0:** `AGENTS.md`, `product.md`, `GITHUB_WEBHOOKS_IMPLEMENTATION_PLAN.md`, `engine/README.md`, código de trabalho listado na seção 2 e screenshot local `gh.png`. Screenshot é fonte da seleção **56**, código local é fonte do visual aprovado.
- **S1:** https://docs.github.com/en/webhooks/webhook-events-and-payloads — página consultada durante este planejamento; nomes/ações/objetos principais, disponibilidade, headers, nulabilidade e relações de issues. Usar âncora correspondente ao eventType (ex.: `#issue_relates_to`). As listas de ação documentadas não são necessariamente equivalentes à descrição curta do screenshot.
- **S2:** repositório oficial https://github.com/github/docs, commit observado `602021b0ba16b4a90e9c0a962a67ca9744eac82e`, arquivos em `src/webhooks/data/ghec/`:
  - `bypass_request_secret_scanning.json` e `.child-params.json`;
  - `dismissal_request_dependabot.json`, `dismissal_request_code_scanning.json`, `dismissal_request_secret_scanning.json` e seus `.child-params.json`.
  - Exemplo de URL fixa: https://github.com/github/docs/blob/602021b0ba16b4a90e9c0a962a67ca9744eac82e/src/webhooks/data/ghec/bypass_request_secret_scanning.child-params.json . Confirma `exemption_request.request_type`, `exemption_request_data`, `html_url` e status/responses; não confirma sozinho o nome do header push ruleset.
  - A página pública equivalente usa `https://docs.github.com/en/enterprise-cloud@latest/webhooks/webhook-events-and-payloads`. Não confundir disponibilidade Enterprise com impossibilidade de manter a opção observada no screenshot.
- **S3:** https://github.com/octokit/webhooks/tree/aa80c24a3f55354465b8b1438d33dd5602956d0d/payload-schemas/api.github.com e `payload-examples/api.github.com`. Consultados árvore e enum `common/webhook-events.schema.json`; esse enum está incompleto para screenshot atual e não é lista final. Ler o arquivo de família correspondente ao gerar fixture mais detalhada; este plano não alega validação integral de todos os schemas.
- GitHub HMAC: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries . Contrato local conferido diretamente em `webhooks_api.go`.
- Pesquisa adicional no OpenAPI oficial `github/rest-api-description` e código público não encontrou confirmação canônica do header push ruleset. Não incorporar aliases/contratos encontrados só em código de terceiros como se fossem oficiais.

Durante o planejamento original não houve implementação ou execução. A etapa de implementação e os checks efetivamente executados estão registrados a seguir. Nenhum commit/push foi feito; entrega GitHub real ainda exige conexão externa configurada pelo usuário.

## 12. Entrega do implementador — 2026-10-13

### Código e pequenos ajustes

- `engine/github_activity_catalog.go`, `github_activity.go`, `github_activity_api.go`; três rotas GET em `api.go` e cache volátil no `app`. Sem alteração de store/journal/worker/consumer.
- `githubActivityApi.ts`, `useGithubActivityFeed.ts`, `ActivityFeedSettings.tsx`, `GithubActivityCard.tsx`; integração sobre `KlmAgentWorkspace.tsx` e adições locais ao CSS. `mockActivity.ts` não foi editado pelo implementador nem restaurado.
- `scripts/github-webhooks/{fixtures.mjs,simulate.mjs,README.md}`: 124 cenários parciais, 56 IDs, 55 headers confirmados + header sintético documentado.
- Body de Details limitado inicialmente a **24 KiB** (em vez do exemplo 32 KiB) para reservar campos/entries; teto serializado de **64 KiB** aplicado adicionalmente. Resumo normalizado tem teto serializado de 6 KiB antes de metadata, para absorver expansão de escapes JSON e manter ~8 KiB/item. URLs nunca são truncadas.
- Preferência malformada mantém erro explícito; catálogo ainda pode abrir Settings e uma escolha explícita/Enable all recupera a configuração, sem default silencioso.

### Evidência executada

1. `go build -tags dev ./...`: passou; repetido após aprimorar extração/limites do normalizador.
2. `npx tsc -b`: primeira execução apontou import de ENGINE_URL e refs sem initial value; corrigidos. Segunda execução passou.
3. `node scripts/github-webhooks/simulate.mjs --list`: passou, 56 catalog IDs / 124 scenarios / 55 confirmed headers.
4. `node scripts/github-webhooks/simulate.mjs --all --dry-run`: passou, sem rede.
5. Overlay Go temporário fora do repo com API27331: compilou, runtime com dataDir inteiramente novo e dois projetos/conexões sintéticos.
6. Smoke descartável enviou os 124 cenários; verificou 202 novo, 200 duplicado mesmo ID/raw, 401 assinatura inválida, ping fora, 56 IDs projetados, PR merged/closed, CI failure/completed-null, comentários PR/issue, links inválidos não clicáveis, detalhes sem marcadores sintéticos secret/key, string decimal grande (inclusive número JSON literal), paginação limit=2 descendente, high watermark estável após novo ingresso, filtros/categoria/search antes da página, cursor incompatível 400 e dedupe independente por conexão.
7. Reinício **somente** do runtime descartável preservou as 128 ocorrências visíveis e todos IDs/sequências/receivedAt/emittedAt comparados ao relatório anterior.
8. `git diff --check`: sem erros de whitespace; Git emitiu apenas avisos LF/CRLF já usuais neste workspace.

Sem suítes, testes de integração versionados, agentes de review ou navegador. Não se declarou validação UI pelo build.

### Runtime deixado para a origem

- API: `http://127.0.0.1:27331` (17331 não foi parado/reiniciado).
- PID após reinício verificado: `35868`; engine original17331 continuou no PID `29416`.
- Pasta temporária: `C:\Users\Samuel\AppData\Local\Temp\opencode\klm-github-activity-20261013`.
- Executável: `<pasta>\klm-feed-smoke.exe`; dados: `<pasta>\data`; projetos vazios `<pasta>\project-alpha`, `<pasta>\project-beta`.
- Auxiliares temporários: `overlay.json`, `runtime_dev_smoke.go`, `smoke.mjs`, `external.json`, `report.json` (relatório sem secret) e `stop-smoke.ps1` (parada restrita ao dataDir descartável). O secret foi gerado apenas em memória e persistido pelo engine somente na conexão descartável; não está no script/relatório.
- `smoke.mjs` sem argumento é setup de uso único e falha se dataDir já existe; `verify-restart` para e inicia apenas esse binário com DATA_DIR explícito. Não reexecutar para criar mais fixtures.
- Para parar: definir `KLM_DEV_DATA_DIR` exatamente para `<pasta>\data` no processo que invoca `<pasta>\klm-feed-smoke.exe stop`; o canal de controle é derivado desse diretório. Nunca usar o CLI do engine real.
- A origem pode usar Vite separado em 5273 com `VITE_ENGINE_URL=http://127.0.0.1:27331`. O implementador não alterou o browser/origem5173.

### Pendências na entrega do implementador (antes da validação da origem)

Playwright/aceitação visual dos cards, Settings/reload/storage, bookmarks/Details/âncora durante refresh, múltiplas páginas novas enquanto aberto, conexão disable/delete e projeto remove/restore na UI, TTL decorrido, comportamento real de GitHub/plano/escopo e confirmação canônica do header push ruleset bypass. O smoke não certifica essas verificações não executadas.

## 13. Validação pontual da origem — 2026-10-13

- A tentativa de iniciar outro Vite por `Start-Process` ficou pendurada e foi interrompida pelo usuário. A origem mudou de estratégia: usou os assets do Vite existente em 5173 num **contexto Playwright separado**, com requisições de API redirecionadas somente nesse contexto ao engine descartável27331. Nenhuma preferência do navegador normal foi alterada. O override VITE_ENGINE_URL do Vite existente prevalecia sobre localStorage, daí a necessidade do redirecionamento no contexto de teste.
- Paginação 50 → 100 → 128 cards únicos; provider GitHub em todos. PR com título/branches/referência/link do payload; Details sob demanda e bookmark mantido ao trocar categorias.
- Feed settings: 56 opções habilitadas; busca de tipo, desmarcar Pull requests, persistir após reload, Enable all e fechamento com Escape. Busca de evento com número decimal grande, estado vazio e Clear filters conferidos.
- Layouts 1440, 900 e 390px: cards/metadados sem overflow horizontal nas amostras carregadas; dialog dentro do viewport e rolável.
- Chegada de 105 POSTs assinados por uma conexão descartável nova: a janela montada passou de 128 para 233 cards únicos, preservando os128 anteriores. A âncora visível deslocou apenas ~0,17px. Remoção dessa conexão retornou a janela aos128 cards anteriores.
- Resposta503 simulada na leitura do feed preservou os cards carregados; Retry recuperou. Nenhum erro JS não tratado nas asserções finais de renderização.
- Ajustes derivados: Details mostra **Ready for review** para PR aberta com draft=false; preservada a frase aprovada **A new PR was created by… in…** e as cores do mock para opened/merged. Fixtures de fechamento passaram a ter state=closed, e PRs passaram a ter descrição própria. Checagem Node dos estados opened/merged/closed passou.
- Após as alterações Go, o binário de smoke foi recompilado com overlay27331 e asserções dirigidas de copy, cor e readiness passaram no navegador. Executável final temporário: `klm-feed-smoke-final.exe`. Capturas locais: `feed-pr-validated.png` e `feed-settings-validated.png` na pasta temporária da seção12.
- Ainda pendentes: aceitação humana, entrega GitHub externa, confirmação do header canônico de bypass, TTL decorrido, disable e remove/restore de projeto no fluxo completo, sincronização entre abas. Nenhuma suíte completa ou revisão adversarial executada.
- Ao concluir, o contexto Playwright isolado foi fechado e o engine27331 foi parado pelo script restrito ao dataDir descartável. Dados/capturas temporários foram preservados; engine17331 e Vite5173 originais não foram reiniciados.
