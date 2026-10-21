# Plano de implementação — board global de atividades

## 1. Status e finalidade

**Entrega desta tarefa: somente este plano.** Nenhuma funcionalidade foi implementada, nenhum teste/build foi executado e nenhuma revisão adversarial foi realizada. A investigação leu `AGENTS.md`, `product.md` e os pontos de código/documentação indicados abaixo. O diretório compartilhado já contém numerosas alterações; elas não devem ser sobrescritas na implementação futura.

O board será uma interface global sobre tarefas cuja autoria e estado oficial permanecem no GitHub e no Azure DevOps. Configuração local significa fases, conexões, associações e mapeamentos; não significa criar um segundo sistema autoritativo de tarefas.

Legenda deste documento:

- **Confirmado:** requisito humano fornecido para este plano.
- **Verificado:** comportamento observado no código/documentação corrente; não equivale a validação de execução.
- **Proposta:** escolha técnica ou de experiência recomendada, ainda não decisão aprovada.
- **Decisão necessária:** pergunta que bloqueia uma parte concreta da implementação. Não bloqueia a entrega deste plano.

Os contratos novos abaixo são **propostos**, inclusive nomes de campos, rotas e arquivos. Não constituem autorização para implementar.

## 2. Escopo confirmado e limites

### Confirmado

- Um único board e um conjunto **global** de fases criadas pelo usuário, sem limite funcional de quantidade.
- Cada fase precisa de pelo menos um mapeamento em **todas** as conexões/providers conectados.
- GitHub usa labels; Azure DevOps usa estados nativos de work items.
- Vários estados/labels podem apontar para a mesma fase. Exemplo ilustrativo, não configuração automática obrigatória:

  | Fase global | GitHub | Azure DevOps |
  | --- | --- | --- |
  | Backlog | `backlog` | `New` |
  | In progress | `in-progress` | `Active` |
  | Done | `done` | `Closed` |

- Na conexão, o usuário define a prioridade de saída dos estados/labels que correspondem à mesma fase.
- Mudar uma tarefa de fase deve atualizar o provider de origem.
- Issues GitHub sem nenhuma label enquadrada ficam ocultas; não são apagadas nem alteradas.
- KLM pode criar as labels necessárias no GitHub.
- Na página do KLM Agent, um componente abaixo de **Happening now** abre o board global.
- Em cada projeto, um botão abaixo de **New session** abre o **mesmo** board com filtro daquele projeto.
- UI controlada pela aplicação em inglês.

### Fora do escopo desta proposta inicial

- Autoria local de tarefas, criação/edição de título, descrição, responsáveis, comentários ou anexos; abrir o item no provider atende esses usos.
- Transformar tarefas em `GraphActivity`, executar agentes/grafos automaticamente ou conceder permissões a partir de labels/eventos.
- Sprints, swimlanes, WIP, estimativas, dependências, ranking manual de cards, dashboards, histórico próprio de tarefas ou novos providers.
- Framework genérico de plugins, novo banco/broker, infraestrutura de túnel, registro automático de hooks, GitHub App/OAuth completo por antecipação.
- Suporte automático a GitHub Enterprise/Azure DevOps Server antes de decidir o alcance das conexões.
- Drag-and-drop como condição para a primeira fatia: um seletor **Move to** já permite mudar fase com teclado e toque.
- Escritas offline com replay automático.

Hooks foram discutidos como possibilidade, não solicitados para implementação imediata. A primeira fatia deve funcionar por API sem depender deles. A etapa de hooks abaixo é uma evolução proposta, sujeita a autorização posterior.

## 3. Base real do repositório

| Evidência inspecionada | Fato verificado e consequência |
| --- | --- |
| `product.md` — definição, Windows Runtime, Project settings and GitHub Webhooks, KLM Agent | KLM é camada de interface sobre harnesses. Há feed e sessões reais; não há requisito aprovado para execução disparada pelo board. |
| `engine/store.go` — `Project`, `diskState`, `loadState`, `saveState`, `commitLocked` | Projeto tem ID, diretório e remoção lógica, mas não vínculo a repo/Azure. Estado é JSON + journal; loader rejeita campos desconhecidos. Conexões webhook ficam separadas de `Project`. |
| `engine/transactions.go`, `engine/stream_journal.go` | Transações clonam estado e persistem patches com `Sync` antes da publicação; replay e checkpoint existentes. Mapas substituem valores por chave. Não introduzir um segundo caminho de gravação. |
| `engine/abandoned_tasks.go` | O startup remove campos de uma antiga implementação Tasks: `taskSchema`, `tasks`, `taskRuns`, `githubBindings`, `githubReceipts`, `engineTime` e referências em sessões/grafos. **Não reutilizar esses campos nem ressuscitar Tasks antigas.** |
| `engine/webhooks.go` | `WebhookConnection` é registro de ingresso por projeto, com `provider == "github"`, secret HMAC, retenção, ID e habilitação. Não contém credencial REST, inventário de repositórios ou mapeamento de fases. |
| `engine/webhooks_api.go` — `receiveGithubWebhook` | Recebe corpo original limitado, valida HMAC-SHA256, headers únicos, JSON UTF-8; persiste evento + pending antes de 202. Duplicata retida retorna 200. HMAC autentica corpo, não headers de tipo/delivery. |
| `engine/webhooks_worker.go` | Worker independente de clientes libera pending em lotes para registros duráveis. `emitted` significa disponível internamente, não processado por board/consumidor. |
| `engine/webhooks.go` — `listWebhookReleases` | `GET /api/projects/{id}/webhook-events` lê releases ascendentes por `after`, `nextAfter`, `highWatermark` em strings decimais. Não há ACK nem checkpoint persistente de consumidor. |
| `engine/github_activity_api.go` — `githubActivitySnapshot`, `readGithubActivityPage`; `engine/github_activity.go` | Feed deriva de releases ainda retidos de projetos ativos, com cache de apresentação. Não é inventário atual de issues: expiração/remove webhook pode retirar conteúdo do feed. Não usar seus cards/categorias/cursor descendente como fonte do board. |
| `engine/api.go` — `routes`, middleware, `decode` | API pública tem CORS aberto, mutations JSON e limite usual de 512 KiB. Ingresso GitHub tem tratamento especial de corpo bruto. Novas rotas administrativas entram aqui, não na bridge de harness. |
| `engine/linked_bridge.go` — listener e `serve` | Bridge privada em `127.0.0.1`, valida Host/RemoteAddr loopback, ausência de Origin e bearer. Não alterar `loopbackHost` nem expor credenciais/provider por essa bridge. |
| `engine/mcp_secrets_windows.go`, `engine/mcp_secrets_other.go` | Há precedente de proteção DPAPI vinculada ao usuário Windows; outras plataformas falham explicitamente. As funções/records atuais pertencem ao MCP. Reaproveitar técnica, não presumir que MCP OAuth/credenciais servem ao board. |
| `clients/desktop/src/App.tsx` | Navegação por estado `view` (`chat/general/design/agents/graphs`), sem necessidade de novo roteador. Sessão/projeto selecionados, overlays e Focus já têm estado próprio. |
| `clients/desktop/src/features/klm-agent/KlmAgentWorkspace.tsx` — `ActivityFeed` | `.activity-discovery` contém busca e `<HappeningNow />`: ponto preciso para adicionar o acesso abaixo da lista. `HappeningNow` também é reutilizado pelo widget Focus; não colocar o acesso dentro dele indiscriminadamente. |
| `clients/desktop/src/features/workspace/WorkspaceSidebar.tsx` | `sidebar-navigation` começa em **New session**, depois Agents/Graphs. Adicionar callback/entrada logo após New session e ajustar o tipo de `activeSection`. |
| `clients/desktop/src/features/projects/ProjectSettingsDialog.tsx`; `features/webhooks/api.ts` | Configuração atual de Webhooks continua em Project settings. DTO retorna `secretConfigured`, nunca o secret. Não converter esses webhooks silenciosamente em conexões REST. |
| `clients/desktop/src/engine.ts`, `platform.ts` | `request` e `ENGINE_URL` centralizam HTTP; base respeita desktop/browser, override e engine remoto. `IS_DESKTOP` é Tauri; `IS_MOBILE_HOST` identifica Flutter, não largura da tela. |
| `clients/desktop/src/design-system/*` | Existem Button/IconButton, Input, Select, SearchSelect, Menu, Badge, Section, Toggle, TabNav e tokens. SearchSelect é seleção única; mapeamento múltiplo exige composição na feature, não presumir multiselect pronto. |
| `engine/README.md` — webhooks; `GITHUB_WEBHOOKS_IMPLEMENTATION_PLAN.md` | Contratos de ingresso, retenção, limites, setup manual e cursor documentados. O plano antigo tem descrições históricas de UI que já mudaram: preferir código corrente + product.md. |

A pesquisa nos módulos atuais não encontrou cliente REST GitHub/Azure para tarefas, receptor Azure ou implementação de Data Sources aproveitável como contrato do board. Isso não deve ser confundido com inexistência dos webhooks GitHub já entregues.

### Limitações importantes da infraestrutura existente

- Deduplicação GitHub é `(webhookId, deliveryId)` **somente enquanto o evento estiver retido e não expirado**; outra conexão ou entrega após TTL pode produzir novo evento.
- Ordem de recebimento/liberação é ordem do engine, não ordem causal do provider.
- Cleanup remove evento, pending e release. O cursor não prova que todos os eventos anteriores foram vistos: existem lacunas por TTL/exclusão e sequências globais de outros projetos.
- Remover projeto preserva identidade/configuração, bloqueia reads/ingresso e pausa pending; TTL continua. Desabilitar webhook bloqueia novas entregas, mas libera pending já aceitos.
- SSE e wake channels não são log durável. GitHub não tem redelivery automático presumido pelo contrato atual.
- O secret HMAC atual não é token GitHub. Secrets webhook atuais não têm criptografia em repouso prometida.

## 4. Decisões necessárias antes das fatias afetadas

### D1 — Que recurso pertence a qual projeto? O que conta como conexão?

**Bloqueia configuração, identidade do filtro e inventário.**

Proposta mínima: conexão de board seleciona explicitamente um recurso e um projeto local: GitHub repository; Azure organização/collection + project + conjunto de tipos incluídos. Não inferir por git remote, diretório, título, credencial, ID de webhook ou projeto ativo na UI. Inicialmente, um mesmo escopo externo não deve ser cadastrado com mapeamentos concorrentes nem ser associado a vários projetos locais; exibir conflito em vez de duplicar cards. Se for necessário multi-projeto por recurso, definir regra explícita antes de implementar.

Proposta de nomenclatura: `BoardConnection` é conexão de dados/API; `WebhookConnection` continua conexão de ingresso. A exigência de cobertura de fases se aplicaria a **todas as conexões de board cadastradas**, inclusive indisponíveis/pausadas, e não a cada webhook genérico de organização já existente. Confirmar essa interpretação: webhooks antigos não possuem escopo REST suficiente para configurar fases automaticamente.

Para Azure, decidir tipos abrangidos. Estados variam por processo e tipo; a sugestão mais simples para a primeira conexão é selecionar um tipo explicitamente, repetindo para outros se necessário. Se múltiplos tipos forem incluídos na mesma conexão, o mapeamento precisa qualificar o tipo e a saída precisa ser aplicável ao tipo real do item, sem fallback arbitrário.

### D2 — Autenticação e alcance dos providers

**Bloqueia qualquer leitura/escrita real.**

Proposta para validar rapidamente: GitHub.com com fine-grained PAT limitado aos repositórios escolhidos e permissão Issues read/write; Azure DevOps Services com PAT de Work Items read/write restrito à organização e ao menor acesso viável. Confirmar disponibilidade desses métodos nas contas/políticas do usuário. Leitura pode funcionar com menos permissão; a conexão precisa diagnosticar capacidade insuficiente para mover/criar labels.

Não usar secret de webhook como token nem extrair tokens do harness, CLI `gh`, MCP ou configuração nativa. GitHub App/OAuth, Entra ID e hosts privados só entram se o usuário precisar deles; nesse caso alteram esta fatia antes da implementação. Token de leitura/escrita de tarefas não implica permissão de administrar Service Hooks.

### D3 — Leitura ambígua e cobertura de fases

**Bloqueia classificação final e publicação da configuração.**

Uma issue com `backlog` e `in-progress` não tem fase única determinada pelos requisitos. A prioridade **de saída** não resolve isso.

Proposta simples: não escolher fase automaticamente; manter o item fora das colunas normais e oferecer lista compacta **Mapping conflicts**, com link externo e ação humana **Move to** para resolver, quando a mutação estiver disponível. Não chamar isso de issue sem mapeamento: ocultar sem aviso esses conflitos seria uma política diferente. Alternativa que exige aprovação: prioridade de leitura separada. Nenhuma dessas políticas está aprovada.

Também é proposta, não requisito confirmado, impedir que a mesma label/estado externo aponte para duas fases dentro do mesmo escopo. Recomendação: unicidade reversa por conexão e, em Azure, tipo de work item.

**Tensão a resolver:** não impor teto numérico de fases no KLM é possível, mas Azure possui um conjunto finito de estados. Cobertura obrigatória de todas as fases + unicidade reversa limita as fases representáveis nesse provider. Se “sem limite funcional” exigir mais fases que estados nativos distintos, esses três pontos são incompatíveis sem mudar a semântica. Não criar estados Azure, duplicar o mesmo estado entre fases ou reduzir o requisito silenciosamente. Confirmar que a ausência de teto do produto convive com essa limitação real de mapeamento; caso contrário, rever D3.

### D4 — Labels versus `open/closed` no GitHub

**Bloqueia o contrato da mutação GitHub e o filtro de inventário.**

Proposta mínima: fase depende exclusivamente das labels; consultar issues abertas **e fechadas**, mantendo `open/closed` como metadado visível. Mover para Done não fecha issue, e mover para outra fase não reabre. Isso evita inventar semântica de fases pelo nome. Se o usuário desejar close/reopen, precisará de configuração explícita e tratamento de efeitos parciais; não está nesta primeira proposta.

### Não precisam bloquear a primeira fatia, se aceitas as opções conservadoras

- **Transição inválida Azure:** retornar erro factual do provider, manter/refazer leitura do estado real, não escolher automaticamente a segunda prioridade e não contornar regras. Exigir campos adicionais abre o item no Azure; editor genérico de campos fica fora.
- **Ordem e remoção de fases:** inicialmente ordem de criação e renomeação por ID; reordenação/remoção adiadas até definir comportamento. Não apagar estados/labels nem mover tarefas automaticamente. Sem rank local de cards.
- **Offline:** exibir último snapshot confirmado com estado **Out of date** e bloquear escrita quando indisponível; refresh/reconciliação antes de nova tentativa, sem fila automática de writes. Isso é proposta de recuperação, não garantia de modo offline.

## 5. Configuração e projeção mínimas propostas

### 5.1 Configuração local autoritativa

Guardar separadamente no `diskState`, usando nomes novos com prefixo `activityBoard`:

| Registro | Conteúdo mínimo |
| --- | --- |
| `activityBoardConfig` | Versão própria do schema, revisão da configuração e lista ordenada de fases `{id, name}`. IDs estáveis gerados no engine; nomes não são identidade. |
| `activityBoardConnections` | `{id, provider, name, projectId, scope, mappings, credentialRef, ingressRefs?}`. `scope` usa IDs canônicos validados no provider e locators para chamadas. `ingressRefs` só é necessário na etapa de hooks. |
| `mappings` por conexão | Para cada `phaseId`, lista **ordenada** de labels/estados de saída; aceitar múltiplos valores de entrada. GitHub guarda ID da label e nome atual; Azure guarda identificador/nome exato do estado e tipo. Não tratar categorias Azure como estados. |
| `activityBoardCredentials` | Blob protegido por usuário Windows separado dos DTOs; somente referência/configured state na configuração pública. Não compartilhar namespace de credenciais MCP. |

Não precisa de tabela de boards nem `boardId`: há um único board por engine, independente do projeto selecionado. Não persistir `ProjectID` dentro das fases. O campo `projectId` da conexão depende da aprovação de D1.

**Invariantes de configuração:**

1. Toda conexão cadastrada precisa ter lista não vazia para cada fase, com referências válidas no escopo provider. Validar no servidor, não apenas no formulário.
2. Adicionar fase exige enviar os mapeamentos de todas as conexões no mesmo commit. Adicionar conexão exige mapear todas as fases existentes antes de ativá-la. Rascunho incompleto fica no formulário, não conta como conexão efetiva.
3. Configuração tem revisão esperada para evitar que duas telas salvem matrizes incompatíveis. Criar fase e cadastrar conexão concorrentemente deve ser revalidado sob o mesmo lock.
4. Sem conexões, fases podem ser criadas; a primeira conexão deve cobrir todas antes do commit. Não criar exemplos automaticamente.
5. Pausa/erro de rede não dispensam cobertura. Se label/estado desaparecer externamente, manter configuração para recuperação, sinalizar **Mapping required**, impedir a saída afetada e não fabricar um novo estado válido.
6. Falha de provider durante validação não autoriza guardar configuração incompleta. Não manter `app.mu` enquanto faz HTTP: capturar configuração/revisão, consultar, depois revalidar e persistir.
7. Não introduzir teto de negócio para fases. Limites técnicos de payload/paginação continuam explícitos; operações incrementais evitam reenviar toda a configuração em cada alteração, sem truncar fases ocultamente.

### 5.2 Cache/projeção técnica, não tarefa local

Proposta: persistir snapshot compacto por identidade externa para leitura do board, boot e indicação de dados antigos. Não guardar corpo/comentários/histórico bruto como segunda cópia da tarefa.

`activityBoardItems[externalKey]` contém apenas:

- Provider/namespace, ID externo estável, escopo/connection responsável e locator atual.
- Referência humana (`#123`, work item ID), título curto, URL validada, tipo.
- Labels/estado nativo necessários à classificação; GitHub `open/closed` separado.
- `providerRevision?`, `providerUpdatedAt?`, `etag?`, `observedAt`, estado de disponibilidade.

`phaseId` é derivado da configuração e dos dados externos, não uma coluna editável independente. Se materializado por eficiência, carregar também a revisão de mapeamento e recalcular ao alterá-la. Não converter um clique diretamente em alteração permanente de `phaseId`.

`activityBoardSync[connectionId]`: último sync completo bem-sucedido, geração de leitura, erro recuperável sanitizado e indicador de snapshot incompleto/antigo. Cursor/progresso de releases e conjunto de itens dirty só entram quando hooks forem implementados.

`activityBoardMoves[operationId]`: recibo pequeno de intenção/resultado da mudança (identidade, fase solicitada, revisão observada, saída escolhida, estado pending/confirmed/failed/unconfirmed). Necessário para não repetir uma escrita após timeout/restart sem verificar. Não é um workflow ou histórico autoritativo da tarefa. Definir retenção limitada de recibos concluídos; nunca eliminar pendentes como se concluídos. A janela de dedupe local deve ser documentada, não prometida como infinita.

### 5.3 Identidade e deduplicação

- GitHub: namespace do host + ID estável da issue (`id`/`node_id`, padronizar um). Repository ID e número são locator/escopo; número sozinho, URL e título não são identidade. Transferência/rename exige refetch e atualização do locator/vínculo, não segunda tarefa.
- Azure: namespace estável de organização/collection + work item ID; o ID não é global entre organizações. Project/type são escopo atual e podem mudar. Não comparar revisions de namespaces distintos.
- Credencial, conexão de ingresso e projeto local **não** entram na identidade canônica da tarefa. Duplicatas via dois hooks ou troca de token atualizam o mesmo item.
- Até existir política multi-associação, impedir conexões com escopos sobrepostos para o mesmo recurso; não escolher credencial/mapeamento vencedor por ordem de cadastro.
- Eventos usam identidade de entrega separada da identidade de tarefa; repetir evento pode disparar uma leitura redundante, mas não criar card duplicado nem repetir uma mudança de fase.

## 6. Leitura por API e classificação

### GitHub

Novo módulo pequeno `engine/activity_board_github.go`, sem framework de plugins:

1. Validar acesso e obter ID/locator canônico do repositório; listar labels paginadas.
2. Listar issues por repositório usando a API REST e `state=all` se D4 for aprovada; filtrar pull requests que também aparecem nos endpoints de issues. Paginar integralmente, sem limite silencioso nas primeiras 100 tarefas.
3. Trazer só os campos da projeção. Para card revalidado/mudança, ler a issue específica.
4. Cruzar labels com mapeamentos: zero fases → oculto; uma fase → coluna; vários valores da mesma fase → uma coluna; várias fases → política D3 ainda pendente.
5. Renome de label com ID conhecido atualiza locator, não cria label duplicada. Label apagada invalida mapeamento até correção. Não fazer matching por nome da fase.

### Azure DevOps

Novo módulo `engine/activity_board_azure.go`:

1. Resolver organização/project e tipos escolhidos; consultar catálogo de estados por tipo/processo via API suportada. `New/Active/Closed` são exemplos, não universais.
2. Usar WIQL para IDs do escopo, seguido de leitura em lotes dos campos mínimos (`System.Title`, `System.State`, `System.WorkItemType`, `System.TeamProject`, revisão e URL).
3. Não assumir paginação arbitrária de WIQL: há limites do provider. Consultar IDs em intervalos ordenados (por exemplo `System.Id > últimoID`) ou outra estratégia suportada e documentada na implementação, dividindo lotes de detalhes no limite oficial. Excesso/resultado parcial precisa aparecer como incompleto, nunca como inventário total.
4. Classificar pelo estado real qualificado por tipo. Estado desconhecido gera diagnóstico de mapeamento; não inventar coluna ou equivalência por categoria/nome semelhante.
5. Resultado parcial, 401/403, falha de lote ou item inacessível não significam exclusão. Deleted/restored/moved exigem revalidação no provider.

### Reconciliação mínima confiável

- Primeiro carregamento, conexão criada/alterada, reinício e ação **Refresh** devem permitir recarga completa do escopo. Proposta adicional simples: refresh ao focar a página, com coalescência e intervalo mínimo para não saturar o provider.
- Um sync por conexão por vez, com timeouts, cancelamento, paginação, concorrência pequena e respeito a `Retry-After`/rate limits. Reads podem ter retry limitado com backoff; writes não têm retry cego.
- Resultado de scan usa geração. Só aposentar itens ausentes quando o scan inteiro tiver concluído para o mesmo escopo/configuração e sem erro; não apagar snapshot bom por falha parcial.
- Paginação externa não equivale a snapshot transacional: mudanças durante o scan podem exigir nova leitura. Sobreposição de janela/deduplicação e reconsulta dos itens dirty mitigam isso sem prometer ausência total de corrida.
- Na primeira fatia, **Refresh** pode sincronizar em background e retornar `syncing`; o board continua mostrando snapshot anterior rotulado. Carregar/configurar uma conexão não precisa bloquear chats enquanto lista milhares de issues.
- Cache de tarefas não herda TTL do webhook. Expirar um evento não deve remover a issue do board. Remover conexão elimina vínculo/cache daquele escopo, nunca o objeto remoto.

## 7. Mudar fase no provider

### Fluxo comum proposto

1. Frontend envia `operationId`, `phaseId`, revisão de configuração e versão observada do item. Engine resolve provider/escopo pela identidade canônica, nunca por URL arbitrária enviada pelo cliente.
2. Validar fase, cobertura, conexão, projeto ativo e ausência de outra mudança em andamento para esse item. Registrar intenção durável antes do efeito externo. Uma falha de persistência impede o envio.
3. Fazer leitura atual e conferir conflito com o observado. Se o estado relevante mudou, retornar **409 / Item changed. Refresh and try again.**, em vez de sobrescrever silenciosamente.
4. Resolver saída pela prioridade configurada. **Proposta:** usar o primeiro valor aplicável ao tipo do item; não tentar automaticamente o segundo se o provider rejeitar o primeiro. Para pedido da fase já atual, preferir no-op confirmado por leitura, sem normalizar labels/estado desnecessariamente.
5. Fazer a escrita e obter estado final confirmado por resposta autoritativa e/ou releitura. Atualizar projeção + recibo em commit; UI só mostra sucesso depois dessa confirmação.
6. Timeout, perda de resposta, cancelamento ou falha local após efeito remoto → **Update unconfirmed**. Manter recibo e refazer leitura antes de oferecer nova tentativa. Reinício nunca repete pending automaticamente como se não tivesse enviado.
7. Mesmo `operationId`/conteúdo devolve recibo; conteúdo diferente conflita. Isso deduplica solicitações locais, **não garante exactly-once no provider**. Constatar fase desejada depois de timeout confirma estado observado, não prova que a nossa tentativa foi a causa.

### Escrita GitHub

**Proposta a aprovar:** preservar todas as labels fora do conjunto de fluxo, adicionar a primeira label da fase de destino e remover somente outras labels configuradas como fluxo. Não reescrever título, responsáveis ou `open/closed` sob D4.

Preferir operações de adicionar/remover labels específicas em vez de substituir a lista completa a partir de um snapshot velho, pois a substituição pode apagar labels não relacionadas adicionadas por terceiros. Adicionar a label de destino antes de remover antigas reduz o período sem classificação, mas cria conflito transitório. Essas chamadas não são uma transação: um passo pode falhar após outro ter funcionado.

Serializar mudanças locais do item, suspender publicação de classificação transitória enquanto a operação local estiver em andamento e reler ao terminar/falhar. Outros clientes GitHub ainda podem competir; `updated_at`/ETag de leitura não são CAS de escrita garantidos. Não alegar atomicidade nem ausência de lost update para labels de fluxo. Em falha parcial, expor o estado real e recuperação humana, sem rollback que apague mudanças externas.

**Create label:** ação explícita no setup, usando endpoint de labels do repository com nome validado e cor padrão proposta. Se já existir, reler e vincular; se faltar permissão, manter formulário e mostrar erro. Criar label remotamente e salvar configuração local não são atômicos: se o commit local falhar, informar que a label existe e recuperá-la por leitura; não deletá-la como compensação automática.

### Escrita Azure

Atualizar somente `/fields/System.State` por JSON Patch da API Work Items. Incluir operação `test` sobre `/rev` conforme contrato oficial da API e usar revisão da leitura corrente. Se falhar por concorrência, recarregar e pedir nova ação; não atualizar à força.

Validar que o estado de destino pertence ao tipo. Regras do processo, campos obrigatórios, permissões e transições podem rejeitar mesmo um estado presente no catálogo. Exibir erro sanitizado; manter estado confirmado; não usar bypass de regras, não criar estados, não tentar caminhos intermediários ou estados de prioridade inferior automaticamente.

## 8. APIs locais propostas

Namespace separado: `/api/activity-board`. Não reutilizar `/api/activity/github` (feed) nem APIs de graph activities. DTOs explícitos, sem secrets, payloads externos completos ou objetos internos de persistência.

| Método/rota | Contrato mínimo proposto |
| --- | --- |
| `GET /api/activity-board/config` | `{revision, phases, connections}`; conexões com escopo, mapeamentos, `credentialConfigured` e diagnóstico, nunca credencial. |
| `PATCH /api/activity-board/config` | `{expectedRevision, changes}`; operações limitadas de adicionar/renomear fase e substituir seus mapeamentos por conexão. Adição só publica se a matriz resultante for completa. Não habilitar remove/reorder nesta primeira versão. |
| `POST /api/activity-board/connections` | Provider, projeto local, escopo, credencial e mapeamentos de todas as fases, com revisão esperada. Validar tudo antes de commit; retornar DTO seguro. |
| `PATCH /api/activity-board/connections/{id}` | Ajustar mapeamentos/credencial com revisão esperada. Omitir segredo mantém atual; substituir é explícito. Troca de escopo/provider não é edição silenciosa: tratar como nova conexão. |
| `DELETE /api/activity-board/connections/{id}` | Remoção confirmada, sem remover recurso remoto/hook existente. Recusar enquanto houver mudança pendente não resolvida; limpar projeções e refs futuras de forma consistente. |
| `POST /api/activity-board/discover` | Validar escopo/credencial informados e consultar catálogo limitado/paginado para setup antes de salvar; não persistir rascunho nem refletir token. Pode usar conexão existente por ID. |
| `POST /api/activity-board/connections/{id}/labels` | GitHub somente; criação explícita de label faltante. Para conexão nova, validar com labels existentes e oferecer criação ainda no setup por variante autenticada de discovery, sem ativar conexão incompleta. |
| `GET /api/activity-board/items?projectId=&phaseId=&cursor=&limit=` | `{items, nextCursor?, hasMore, projectionRevision, sync}`. Sem projectId = global; com ID = mesmo board filtrado no engine. Retornar fases vazias pela configuração. |
| `POST /api/activity-board/refresh` | `{connectionId?}`; agenda/coalesce leitura, retorna estado de sync, sem prometer atualização instantânea. |
| `POST /api/activity-board/items/{key}/phase` | `{operationId, phaseId, expectedConfigRevision, observedVersion}`; retorna recibo e item confirmado quando disponível, ou estado unconfirmed/failed sem sucesso fictício. |
| `GET /api/activity-board/moves/{operationId}` | Recuperar resultado após timeout/restart sem reenviar mutação. |

A sintaxe de `changes` deve ser fechada entre backend/frontend antes de codificar; não criar uma linguagem genérica de patch. Criação de label pré-conexão pode compartilhar handler interno do provider com a rota de labels, mantendo auth write-only e limites iguais. A UX não pode exigir salvar uma conexão incompleta para criar a label que falta.

Paginação local deve vincular cursor ao filtro e revisão da projeção; revisão alterada invalida cursor com erro recuperável e reinício da janela. Proposta inicial: paginação por coluna/fase, ordem estável por identidade externa, sem ranking de prioridade inventado. Não carregar todos os cards de todas as colunas na primeira renderização. IDs/contadores grandes trafegam como strings, como já ocorre no feed.

Erros: 400 configuração inválida; 404 recurso local fora de escopo; 409 conflito de revisão/operação; erro sanitizado de autenticação/provider; 503 engine sem persistência. Usar código de erro estruturado e estado recuperável no cliente, sem expor resposta bruta com dados sensíveis. Ajustar tratamento JSON de DELETE ao middleware existente, sem relaxar regras globalmente.

## 9. Hooks e reconciliação — evolução opcional

### 9.1 GitHub existente

Não mudar o contrato de recepção HMAC nem chamar a API GitHub dentro da transação de ingresso.

- Vincular explicitamente uma ou mais conexões webhook existentes à conexão do board, verificando projectId/escopo aprovado em D1. Recebimento de organização pode carregar recursos fora do board: filtrar pelo repository ID real no corpo, não só pelo projeto do webhook ou header.
- Novo consumidor no engine lê releases ascendentes retidos, com progresso próprio. Não usar a preferência de categorias do feed nem consumir/remover eventos de outros leitores.
- Na mesma transação, registrar item/recurso como dirty e avançar o cursor de consumo. Worker lê API e atualiza snapshot antes de limpar dirty. Se o evento não trouxer identidade suficiente, marcar o escopo para reconciliação, não inventar identidade.
- Dirty pode ser set coalescido por item, sem copiar payload. Restart reconstrói trabalho a partir do estado; novo evento durante leitura precisa incrementar geração dirty para uma leitura antiga não apagar a invalidação nova.
- Considerar ações de issues e alterações relevantes de labels/repository (inclusive renome/remoção), não apenas labeled/unlabeled. Pull requests continuam fora.
- Dedup de ingresso existente permanece limitada à retenção. O consumidor precisa tolerar eventos duplicados entre hooks e reaceitação após TTL; aplicar sempre upsert por identidade, não produzir comandos de fase.

**Sem promessa de completude por cursor:** TTL, conexão removida/desabilitada, projeto removido e indisponibilidade podem causar perda de incrementos. Lacunas numéricas não identificam precisamente perda, pois sequências são globais. Fazer reconciliação completa no startup/reconexão, após mudança de vínculo e periodicamente com cadência interna conservadora; manter **Refresh**. Sucesso de leitura só avança a marca de último sync completo quando realmente completo.

### 9.2 Azure DevOps Service Hooks

O equivalente discutido é Service Hooks/Web Hooks, com `workitem.updated`, filtro de mudança de `System.State` e eventos de criação/exclusão/restauração. Isso é contexto humano, não infraestrutura existente neste repositório.

Ao implementar futuramente:

1. Confirmar na documentação oficial os nomes/formato exatos dos eventos (`workitem.created`, `workitem.updated`, `workitem.deleted`, `workitem.restored`), identidade de evento/subscription, revisões e opções de filtro da versão usada. Validar com payload real da organização.
2. Filtro exclusivo em State não atualiza título/tipo/projeto; ou aceitar esses campos via reconciliação, ou assinar updates mais amplos. Não prometer card inteiramente atualizado com filtro só de State.
3. Criar ingresso público específico, por exemplo `POST /api/webhooks/azure-devops/{ingressId}`, com validação/auth próprias. Não enviar Azure para `receiveGithubWebhook` nem aceitar sem autenticação por ausência de HMAC.
4. Proposta de autenticação: HTTPS e credencial HTTP Basic exclusiva do receptor, se suportada pela configuração Service Hooks adotada. Confirmar suporte/encaminhamento pelo proxy antes da entrega. Nunca usar PAT do Azure no receptor nem afirmar assinatura do corpo equivalente a HMAC GitHub.
5. Reutilizar padrões de corpo limitado, admissão, persistência antes de ACK, worker e dedup. Estruturas/validadores webhook atuais exigem GitHub: generalizar somente o armazenamento comum necessário e manter validações/provider DTOs explícitos, preservando endpoint e comportamento existentes.
6. Dedup por identidade estável de evento do Azure dentro da subscription/ingresso, confirmada no contrato; não por request ID inventado ou timestamp. Retenção e limites precisam ser explícitos. Não criar ledger ilimitado nem presumir retry eterno do Service Hooks.
7. Eventos invalidam cache e causam leitura API. Exclusão/restauração deve respeitar revisão/consulta atual; 404 isolado pode ser falta de acesso, não prova automática de exclusão.

### 9.3 Ordem, revisão e convergência

- `receivedSequence`/`releaseSequence` ordenam transporte local, não estado remoto. Nunca usar “último webhook recebido vence”.
- Azure possui revisão por work item; não substituir projeção com revisão menor. Evento antigo só dispara leitura atual, não desfaz movimento mais novo.
- GitHub não fornece revisão monotônica geral da issue equivalente a `/rev`. `updated_at` pode empatar e ETag é token de cache, não contador causal. Serializar/refazer reads por item e checar geração de sync/configuração antes de publicar; eventos servem como invalidação.
- Durante bootstrap, capturar marco de releases, fazer scan e depois processar incrementos/dirty; reconsulta final cobre alterações concorrentes observadas. Nenhum marco elimina perda por TTL ou garante snapshot transacional do provider.
- Leituras antigas iniciadas antes de uma mutação local/configuração nova não podem sobrescrever a confirmação mais recente: usar geração por item/conexão e releitura. Falhas mantêm snapshot marcado antigo, nunca fabricam fase concluída.

## 10. UI e integração no frontend

Criar `clients/desktop/src/features/activity-board/` com `ActivityBoardPage.tsx`, `ActivityBoardEntry.tsx`, `BoardSettingsDialog.tsx`, `api.ts`, `useActivityBoard.ts` e CSS próprio. São divisões simples de responsabilidade, não um novo design system.

### Entradas e navegação

- `ActivityBoardEntry` imediatamente após `<HappeningNow />` em `.activity-discovery`, somente na página KLM Agent: título **Activity board** e ação **Open board**. Contagem/resumo não são necessários na primeira fatia; não inventar métricas nem consulta separada por sessão.
- `WorkspaceSidebar`: botão **Activity board** logo abaixo de **New session**, callback `onActivityBoard(project.id)`.
- `App.tsx`: adicionar `view: 'activity-board'` e filtro `boardProjectId` separado do `activeProjectId`. Ambas as entradas renderizam a mesma `ActivityBoardPage`, com header **Activity board** e seletor **All projects**/projeto.
- Board global deve funcionar sem sessão ativa e apresentar setup mesmo sem projetos; associação pode exigir cadastrar projeto antes de ativar conexão. Renderizar board antes dos guards de “Create a session”/“Add a project”, sem criar projeto ou sessão fictícios.
- Não retargetar composer, parar sessões ou perder drafts ao navegar. Preservar seleção de chat e oferecer retorno ao KLM Agent/projeto. Sidebar/header devem refletir contexto global versus filtro, não o último projeto selecionado por acidente.
- Flutter reutiliza a página web; respeitar `IS_MOBILE_HOST` para navegação. Não alterar bridge nativa. Não colocar o atalho em todos os widgets `HappeningNow` do Focus por efeito colateral.

### Página e configuração

- Colunas de fases globais, inclusive vazias; cards com título, referência, origem/recurso, projeto quando global, link **Open in GitHub**/**Open in Azure DevOps** e **Move to**.
- Rolagem horizontal para número arbitrário de fases; espaço utilizável em telas estreitas, seletor de fase pesquisável quando apropriado, foco/teclado/toque. Não truncar a lista de fases nem exigir drag-and-drop.
- Header com **Refresh** e **Board settings**. Configuração global em um único diálogo da feature: **Phases**, **Connections**, **Mappings**. Não duplicar fases em Project settings nem mover o painel Webhooks existente.
- Matriz fase × conexão com seleção múltipla composta de SearchSelect + lista dos valores selecionados. **Move up/Move down** explicita ordem de saída sem depender de arrasto. Distinguir nomes iguais em tipos Azure diferentes.
- Setup escolhe provider, recurso e projeto, informa credencial, seleciona mapeamentos e prioridades. Labels faltantes oferecem **Create label** explícito. Não usar placeholders como valores reais de provider.
- Estados úteis: **Connect a provider**, **Add phase**, **No matching tasks**, **Loading…**, **Out of date**, **Mapping required**, **Update unconfirmed**, **Retry**. **Mapping conflicts** somente se D3 for aprovada. Diferenciar erro/parcial de lista vazia.
- Reusar `Button`, `IconButton`, `Input`, `SearchSelect`, `Select`, `Menu`, `Badge` e tokens de cores/bordas/spacing. Reusar padrão acessível de `<dialog>` e retorno de foco de ProjectSettingsDialog. Lógica de provider/mapeamento fica na feature/engine, nunca em primitives.
- `useActivityBoard` carrega apenas enquanto visível, cancela requisições ao mudar filtro e descarta respostas antigas; refresh coalescido e consulta local de status enquanto syncing. Não iniciar N requests externos por card nem assinar streaming de chats para atualizar o board. Sem dependência nova de cache/router por padrão.
- Usar `request`/`ENGINE_URL`; tratar timeout de mutation como incerto apesar da mensagem genérica atual sugerir retry. Consultar recibo antes de reenvio. LocalStorage só para preferências visuais opcionais, nunca credenciais/configuração oficial.

## 11. Persistência, segurança e lifecycle

### Persistência e migração

- Implementar `initializeActivityBoardRecords` e `validateActivityBoardRecords`; ausência de novos campos em estados antigos significa board vazio, não importação automática de webhooks/Tasks antigas.
- Integrar validação em load, após replay e antes de save/checkpoint; validar operações alteradas no commit sem varrer todo o cache em cada comando de chat. Preservar `Version: 2`/journal format atual se compatível com campos opcionais; versionar o conteúdo do board separadamente.
- Usar `commitTransactionLocked` para configuração, projeções em lotes, intenções/recibos e futura fila dirty + cursor. Sem HTTP externo sob `app.mu`; comparar revisões/generations ao retornar.
- Cache compacto reduz custo, mas clones/diffs atuais ainda têm custo proporcional aos registros. Não persistir respostas API completas nem um evento por renderização. Paginação e lotes evitam transações enormes; diagnosticar capacidade sem declarar scan parcial como completo.
- Downgrade para binário que não conhece campos é incompatível com o loader estrito: documentar backup/restauração do diretório do engine, não apagar campos para forçar downgrade. Não recuperar backup antigo de Tasks nem alterar autorizações de grafos.
- Manter `%APPDATA%/klm/engine` versus `engine-dev`; configuração não fica no repositório nem no diretório geral do agente.

### Credenciais e fronteiras

- Proteger novos tokens em repouso com DPAPI no Windows, seguindo o precedente MCP; helper pequeno específico ou extração mínima deliberada, não refactor amplo do MCP. Falhar explicitamente se proteção não estiver disponível; não gravar plaintext como fallback.
- Segredos entram por campos write-only, só ficam no formulário até salvar/fechar e nunca aparecem em DTO, logs, erro bruto, feed, snapshot de harness, prompt, TOML ou localStorage. Redação de Authorization e credenciais em URLs; respeitar redirects para não encaminhar token a outro host.
- Proposta inicial cloud-only permite URLs base fixas/validadas. Não aceitar endpoint arbitrário do payload para fazer fetch autenticado. Checar host/escopo também em links de paginação e resource URLs, limitar resposta/tempo; strings externas renderizadas como texto, não HTML.
- A API pública permanece em `0.0.0.0:7331` (dev 17331), com contrato pessoal sem autenticação de rede/TLS e CORS aberto. DPAPI **não protege** envio do token pela rede nem impede que alguém com acesso à API mande mutações com a credencial armazenada. Configurar pelo cliente local/rede confiável ou HTTPS apropriado; não vender isso como acesso multiusuário seguro.
- Proxy externo deve expor somente rotas de ingresso necessárias; não publicar `/api/activity-board/*` administrativo por causa de hooks. TLS/auth do receptor Azure são próprios; HMAC GitHub não se aplica a ele.
- Bridge privada autenticada loopback permanece intacta. Nenhuma ferramenta de board é adicionada ao agente por este plano; dados externos jamais são autorização/instrução executável.

### Lifecycle proposto

- Projeto removido: ocultar itens associados, pausar sync/writes desse vínculo e preservar configuração para restauração por mesmo ID, coerente com o lifecycle atual. Restauração exige reconciliação API, não confiar em eventos ainda retidos.
- Remover webhook não remove conexão REST nem tarefa; marcar integração incremental desconectada e continuar reconciliação API. Desconectar board não apaga webhook compartilhado ou registro remoto.
- Revogação/expiração de token: registrar erro sanitizado, manter snapshot antigo, impedir novas writes; oferecer substituição da credencial e refresh.
- Mudança de mapeamento recalcula classificação, não modifica tasks remotas. Impedir alteração do mapeamento/remoção de conexão usada por move ainda incerto até resolver, evitando interpretação diferente do mesmo recibo.
- Shutdown cancela trabalho local e preserva pending/unconfirmed; cancelamento de HTTP não desfaz efeito no provider.

## 12. Sequência incremental implementável

### Etapa 0 — Fechar somente os contratos bloqueadores

Resolver D1–D4 para os providers da fatia seguinte. Confirmar interpretação de cobertura global, tipos Azure, escopos e credenciais. Não pedir agora escolhas cosméticas ou arquitetura de automação. Resultado: exemplos reais de repo/work item, fases/mapeamentos e política de conflito, sem tokens no documento.

### Etapa 1 — Fatia vertical GitHub, com API e refresh manual

- Novos modelos/validação/credenciais no engine; inicialização vazia compatível.
- Conexão repository → projeto local, descoberta de labels, fases globais com matriz completa e prioridade; criação explícita de labels.
- Carga paginada de issues e projeção mínima; regras D3/D4 aprovadas.
- Um board funcional compartilhado pelas duas entradas exigidas; seleção de fase, escrita GitHub, releitura, recibo e erro incerto.
- Usar pelo menos duas conexões GitHub na validação para demonstrar que cobertura é global e que conexão nova não deixa fases sem mapa.

**Aceitação manual da fatia:** item aparece uma vez, projeto filtra sem criar outro board, issue sem label mapeada fica oculta e mover atualiza GitHub sem apagar labels alheias. A fatia não é entrega completa dos dois providers.

### Etapa 2 — Paridade Azure DevOps no mesmo board

- Cliente REST de WIQL/lotes, catálogo de estados por tipo e autenticação aprovada.
- Mapeamentos Azure para **todas** as fases já existentes antes de cadastrar a conexão.
- PATCH de State com revisão, diagnóstico de transição inválida e conflitos.
- Board misto GitHub/Azure sem novas fases por projeto e sem adaptar o modelo para espelhar autoria de work items.

**Aceitação manual:** mesma coluna contém origens diferentes; vários estados Azure mapeados à mesma fase classificam corretamente; ordem de saída e rejeição de transição não são confundidas com regra de leitura.

### Etapa 3 — Sincronização incremental por hooks, se autorizada depois

- Primeiro consumidor GitHub sobre releases existentes com cursor + dirty duráveis e reconciliação de recuperação.
- Depois ingresso Service Hooks Azure com autenticação, eventos/revisão/retention confirmados e setup manual documentado.
- Coalescer consultas e manter reconciliação periódica/API de refresh; hooks não viram comandos automáticos.

**Aceitação manual:** duas entregas do mesmo evento, evento atrasado, restart e perda por retenção não duplicam tarefas nem fazem regressão de estado; falhas continuam recuperáveis por API. Isso verifica cenários concretos, não prova entrega exactly-once.

### Etapa 4 — Documentar e validar a fatia entregue

Na implementação futura, atualizar `engine/README.md` e `product.md` somente com contratos efetivamente implementados e evidência separada de aceitação humana. Registrar restrições de provider e setup de credenciais/hooks. Sem ampliar para backlog local, automações ou refactor do feed.

## 13. Caminho de validação futura (não executado nesta tarefa)

### Verificação leve por fatia

- Se Go mudou: um build direcionado do engine, por exemplo `go build -tags dev -o <temporário-absoluto>/klm-board-check.exe .` dentro de `engine`.
- Se TypeScript mudou: uma checagem de tipos com a configuração existente, por exemplo `npx tsc -b` em `clients/desktop`; não rodar também Vite/Tauri e suítes sobrepostas por padrão.
- Não executar `go test ./...`, `npm test`, suites Playwright, revisão adversarial ou testes complexos sem nova solicitação explícita. Builds não comprovam funcionamento externo.

### Roteiro manual de aceitação

Usar engine dev e recursos descartáveis dos providers; nunca executar probes sobre tokens/repositórios reais do usuário sem escopo definido.

1. Estado antigo abre com board vazio; criar fases sem teto artificial, conectar recursos com mapeamento completo. Formulário/engine recusam fase ou conexão sem cobertura e salvamento com revisão antiga.
2. Duas conexões do mesmo provider, depois GitHub + Azure: nenhuma fase vira local ao projeto. Adicionar nova fase exige mapas em todas. Falta de estados Azure apresenta a limitação de D3, não cria estado automaticamente.
3. Ambos os pontos de entrada abrem a mesma página; filtro por projeto funciona e pode voltar a **All projects**. Sem sessão ativa ainda é possível abrir/configurar. Drafts, overlays e sessões não são alterados.
4. Issues abertas/fechadas seguem a política D4 aprovada; PRs não aparecem. Sem label mapeada permanece intocada e oculta. Duas labels da mesma fase geram um card; labels de fases diferentes seguem D3 explicitamente.
5. Criar label faltante, usar existente, negar permissão e recuperar commit local falho sem duplicar/deletar label remota.
6. Mover GitHub preserva labels alheias; saída respeita prioridade. Falha entre adicionar/remover labels e edição concorrente externa mostram estado real, sem falso sucesso.
7. Mover Azure respeita tipo/revisão. Processo com transição inválida/campo obrigatório retorna erro e não tenta outro estado. Revisão concorrente pede refresh.
8. Timeout/restart durante move e clique repetido com mesmo operationId não enviam retry cego. Confirmar recibo, releitura e texto de resultado incerto.
9. Paginar além de uma página; scan incompleto/rate limit não remove cards nem afirma sincronização completa. Trocar filtro durante carregamento não publica resposta da tela anterior.
10. Token expirado, engine offline e retorno à rede preservam último snapshot sinalizado; nada entra em fila de escrita automática. Remover/restaurar projeto e remover conexão/hook têm efeitos locais documentados, nunca exclusão remota.
11. Se etapa de hooks for feita: recepção real GitHub e Azure com auth correta; payload inválido rejeitado; duplicata, reordenação, TTL e restart recuperados por API. HMAC não substitui auth Azure. Conferir que proxy não expõe administração.
12. Desktop, navegador Focus e Flutter WebView: entradas nas posições exigidas, muitas fases navegáveis, teclado/toque, tema claro/escuro, foco e erros legíveis, sem mudança na bridge móvel.
13. Credencial não reaparece em GET/SSE/erros/logs/UI persistida. A proteção em repouso e o contrato de rede pessoal são documentados sem alegar segurança multiusuário.

## 14. Entrega documental e próximos passos

Este arquivo registra investigação estática e uma proposta incremental; não atesta runtime, build ou aceitação de feature. A implementação deve começar pelas decisões bloqueadoras e pela fatia GitHub, preservando o trabalho concorrente já presente no repositório. Hooks, políticas pendentes e requisitos não confirmados continuam explicitamente separados.

Arquivo: `ACTIVITY_BOARD_IMPLEMENTATION_PLAN.md`.
