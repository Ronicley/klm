# Plano de correção — paginação automática em lotes de 25

## Objetivo e decisão

Restaurar o carregamento por proximidade do scroll sobre os contratos atuais de SQLite. Cada requisição de página de uma lista deve pedir **até 25 itens**, inclusive a primeira. Remover os botões de paginação ordinária das superfícies abrangidas e manter recuperação explícita de erros.

No histórico, **item significa evento persistido**, incluindo mensagem, raciocínio e ferramenta; não significa bolha renderizada. Agrupamentos podem produzir bem menos de 25 linhas. Preservar os limites de bytes e a leitura sob demanda de conteúdo grande.

Implementar em etapas pequenas, na ordem abaixo, mas considerar o trabalho completo somente após cobrir o inventário de superfícies. Preservar as alterações locais existentes, especialmente em `App.tsx`, nos seletores e no engine.

## Evidências e consequências técnicas

- A regressão histórica informada está entre `d3a38c3` (observer no topo) e `a255836` (migração SQLite). Usar isso como referência de comportamento; aplicar a correção na arquitetura atual.
- `HistoryLoader.tsx` carrega apenas por clique e só anteriores. `ConversationHistory.tsx` oferece um reset direto para a cauda em `Latest messages`.
- `engine/sqlite_history.go` já aceita `before` e `after` exclusivos e entrega ambos os cursores. `clients/desktop/src/engine.ts` conserva apenas o cursor anterior.
- `sessionState.ts` aceita prepend apenas quando exatamente adjacente e limita a janela a 200 eventos. O refresh também recorta a janela. Isso exige tratar scroll, recorte e atualização em conjunto.
- `scopedConnection.ts` usa `startIndex + 50` para atualizar uma janela antiga. Trocar apenas 50 por 25 não revalida corretamente uma janela maior nem resolve as duas direções.
- `App.tsx` substitui páginas de tópicos e, após quatro páginas de sessões, remove as anteriores. Happening now também substitui a página ativa.
- O feed já reconstrói sua faixa carregada até a fronteira antiga e preserva a âncora. Reutilizar essa semântica; não simplificá-la para um refresh de primeira página.
- Alguns endpoints ignorariam um novo `limit=25`: atividade/solicitações de grafos, histórico de automações e solicitações pendentes têm limites fixos no SQL. Eles precisam de um ajuste pontual de contrato.

## 1. Mecanismo compartilhado pequeno

**Arquivos novos propostos:** `clients/desktop/src/features/pagination/useNearEdge.ts` e `clients/desktop/src/features/pagination/pagination.ts`.

- Centralizar `PAGE_SIZE = 25` e uma margem inicial de proximidade de 160 px.
- O hook recebe o contêiner real de scroll, sentinel/borda, habilitação, identidade da lista, cursor/token de progresso, estado ocupado/erro e callback de carga.
- Usar `IntersectionObserver` com `root` explícito. Reavaliar após uma página aplicada, redimensionamento e retorno à visibilidade. Um sentinel que continua visível não necessariamente gera um novo callback do observer.
- Não guardar coordenadas de scroll em estado React a cada pixel. Usar refs e callbacks estáveis; desconectar observers/listeners na troca de root ou desmontagem.
- A camada compartilhada detecta proximidade e impede disparos redundantes. A feature continua dona do request, cancelamento, cursor, merge, refresh e mensagens de erro. Não introduzir uma biblioteca de cache ou abstração universal de endpoints.
- A aquisição do lock deve ser síncrona, via ref, antes do primeiro `await`. `setLoading(true)` sozinho não impede duas chamadas no mesmo ciclo.
- Identidade inclui engine/geração de conexão, entidade e filtros efetivos. Cancelar a geração anterior e rejeitar respostas antigas também no commit do estado. O `finally` de uma requisição velha não libera o lock de uma nova.
- Serializar paginação e refresh da mesma lista; invalidations durante uma requisição marcam um único refresh pendente. Instâncias que compartilham o mesmo armazenamento, como chat principal e Focus da mesma sessão, compartilham o coordenador da carga, não apenas o booleano visual.
- Fim depende de `hasMore`/cursor do servidor, não de `items.length < 25`. Uma página pode ser curta por limite de bytes.
- Cursor repetido, resposta sem progresso ou incompatível não pode causar loop. Interromper a carga e apresentar recuperação apropriada.
- Erro de paginação pausa essa direção até `Retry`. Rerender, observer, focus ou polling não devem apagar esse bloqueio por acidente. Autenticação continua pelo fluxo existente.
- Preencher um viewport curto sequencialmente enquanto a borda continuar próxima, houver progresso e a superfície estiver visível. Não carregar listas recolhidas nem executar uma varredura geral em background.
- Em históricos com janela limitada, impedir alternância automática topo/fundo provocada pelo próprio recorte. Priorizar a direção da navegação; reposicionamento programático não é nova intenção de navegação. Parar o preenchimento ao atingir a janela limitada sem ganho visual, em vez de percorrer todo o histórico de eventos ocultos.

**Apresentação:** sentinel discreto, `Loading…` com status acessível durante a carga, erro com `Retry` usando os componentes existentes. Não deslocar foco ao carregar. Garantir scroll por teclado no contêiner. Textos de UI em inglês.

## 2. Contrato de 25 no cliente e ajustes mínimos do engine

### Histórico de conversa

Em `clients/desktop/src/engine.ts`:

- Preservar o cursor `after` retornado pelo engine e os limites reais da página no tipo `EventPage`; manter compatibilidade com os nomes existentes enquanto os chamadores são atualizados.
- Conservar revisão, total, `hasMore`/anteriores, `hasNewer` e referências de conteúdo grande.
- Não fabricar cursores somando 25. O cursor `after` é o ordinal do último evento, exclusivo na próxima consulta; não é `endIndex`, que hoje é exclusivo no cliente.

`engine/sqlite_api.go:sqlHistory` e `engine/sqlite_history.go` já aceitam 25. Enviar o limite explicitamente no frontend; não é necessário mudar o default público ou o storage contract 3.

### Endpoints com limites fixos

| Arquivo | Ajuste necessário |
| --- | --- |
| `engine/sqlite_graph_api.go` | Parametrizar limite das solicitações projetadas e das ativações; adicionar `eventLimit` para eventos de uma ativação. Aplicar `limit + 1`, tamanho real e cálculo de anterior/cauda coerentes. |
| `engine/sqlite_automations_api.go` | Fazer `listSQLAutomationRuns` aceitar e aplicar `limit`; retornar `nextOffset` efetivamente consumido. A lista de automações já aceita limite. |
| `engine/sqlite_api.go` | Fazer `sqlPendingPage` aceitar `limit` validado, preservando orçamento de bytes, escopo e erro 409 quando `afterId` não existe mais. |
| Handler de `/delegation/requests` | Conferir o limite desse endpoint especificamente e parametrizar se fixo; `delegation_api.go` de batches/members não prova suporte em requests. |

Manter defaults compatíveis para outros consumidores, limites máximos existentes e proteção de bytes. Usar parâmetros SQL, sem mudar schema, migração, autorização ou execução. Nas listas já parametrizáveis, mudar apenas seus chamadores para 25.

## 3. Chat bidirecional, âncora e streaming

**Arquivos principais:**

- `features/chat/HistoryLoader.tsx`
- `features/chat/ConversationHistory.tsx`
- `features/chat/SideChatPanel.tsx`
- `features/chat/sessionState.ts`
- `features/chat/scopedConnection.ts`
- `features/chat/DelegationActivity.tsx`
- `App.tsx` — aplicação de páginas e coordenação de refresh
- `features/chat/ConversationEvents.tsx` — somente identificação estável de âncora, se necessária

### Fluxo

1. Abrir conversa na cauda, com `limit=25`, e posicionar no fim antes de habilitar o observer superior. Isso evita carregar anteriores apenas porque o DOM ainda está no topo durante a montagem.
2. Aproximar do topo: solicitar `before=<cursor>&limit=25`, inserir anteriores e conservar a leitura.
3. Aproximar do fundo de uma janela antiga: solicitar `after=<cursor>&limit=25`, inserir os eventos seguintes. Remover o reset de `Latest messages` como mecanismo de paginação.
4. Só acompanhar streaming quando a leitura já estava no fim **do histórico real**: distância inferior ao limiar atual de 24 px e ausência de `hasNewer`. Estar no fundo da janela antiga não é estar na cauda.
5. Ao trocar sessão, limpar a intenção de acompanhamento, a âncora e o estado de request da sessão anterior. Aplicar a política inicial à nova sessão.

### Merge e janela

- Substituir a função exclusivamente `prependHistory` por merge direcional de páginas, atualizando todos os chamadores, inclusive `DelegationActivity.tsx`.
- Aceitar páginas adjacentes ou sobrepostas da mesma identidade; deduplicar por identidade de evento/posição absoluta. Não colar faixas separadas por um buraco.
- Preservar a reconciliação por revisão de evento. Uma página antiga não pode sobrescrever um evento mais novo, reduzir o total já conhecido nem regredir cursores da janela atual.
- Manter a janela limitada já existente, com recorte da extremidade oposta à navegação. Calcular os dois cursores a partir da faixa efetivamente retida, não dos metadados de uma página que foi parcialmente descartada.
- Capturar a primeira linha visível e seu deslocamento **imediatamente antes da aplicação**, depois da resposta. Restaurar em `useLayoutEffect`. Se o usuário rolou durante o request, preservar sua posição mais recente.
- Preferir identidade de linha/evento ou grupo visível. A diferença de `scrollHeight` atual só é segura para prepend simples: quando o estado também remove linhas do fundo, ela calcula a compensação errada.
- Não remover a âncora visível para cumprir um recorte automático. Planejar o recorte com a faixa visível; se a janela não puder avançar automaticamente sem perder a leitura, pausar esse avanço até nova navegação. Não criar um cache ilimitado para contornar o problema.
- Programar uma única compensação por aplicação; não somar correção manual e browser scroll anchoring duas vezes.

### Refresh e corridas

- Retirar a fórmula `startIndex + 50` de `readConversationWindow`.
- Registrar a faixa/geração que está sendo revalidada. Revalidar a faixa carregada por páginas de 25, sem buscar histórico fora dela apenas por uma invalidation. Para atualizações frequentes da cauda, reaproveitar páginas estáveis e atualizar a extremidade relevante; resets exigem revalidação da faixa antes de tratá-la como atual.
- Se a janela mudar durante um refresh, não aplicar um reset que recoloque a janela antiga. Reconciliar o overlap válido e agendar a faixa atual ou descartar o resultado obsoleto.
- Se novos eventos excederem uma página, preservar continuidade: consultar a faixa faltante em páginas, ou reposicionar para a nova cauda somente quando já havia acompanhamento da cauda. Nunca fabricar uma sequência contígua a partir de dois intervalos separados.
- Manter o cache/descarte de conversas não abertas de `App.tsx`; bootstrap continua sem carregar históricos de filhos fechados.

### Superfícies compartilhadas

Aplicar o mesmo comportamento ao chat normal, KLM Agent, overlay, Focus, mobile, SubagentView e SideChatPanel. O painel lateral pode compartilhar o controlador de histórico sem mover suas permissões/composer para outro lugar.

Preservar a projeção compacta de subagentes restaurada em `55968d6`: não trocar `EventSequence`, não expandir filhos para contabilizar 25 linhas, não carregar transcript de filho por paginação do pai. Marcadores de âncora devem respeitar os grupos já renderizados.

## 4. Session rail e tópicos

**Arquivos:** `App.tsx`, `features/workspace/WorkspaceSidebar.tsx`, e `features/workspace/topics.ts` somente se a integração do agrupamento exigir.

- Trocar os limites iniciais 100/200 por 25, também nas páginas seguintes e no refresh.
- Manter estados/cursor/erro independentes de sessões e tópicos sob a identidade `engine + projectId + search + filtros`.
- Remover a rotação após quatro páginas. Acumular metadados por ID enquanto o usuário navega e conservar a ordem persistida. Tópicos passam a acumular, em vez de substituir `topics`/`topicOrder` pela última página.
- Colocar a detecção no verdadeiro contêiner de scroll da sidebar, conferido no CSS. A proximidade do fim pode avançar as duas listas independentemente até preencher a superfície ou esgotar seus cursores.
- Na busca, manter a filtragem no servidor antes da paginação. Alterar a busca invalida requests e estado da consulta anterior; a seleção ativa continua identificada por ID.
- No refresh, reconstruir uma faixa contígua até a fronteira anteriormente carregada, não só o mesmo número de páginas: inserções no início podem empurrar itens antigos além desse número. Aplicar o resultado de forma atômica e preservar a âncora da sidebar.
- Com cursores invalidados por revisão/409, recomeçar a leitura controlada da faixa a partir da primeira página, conservando o que já está visível enquanto reconcilia. Não repetir indefinidamente sob mudanças contínuas; falha persistente deve ter recuperação explícita.
- Coordenar refresh e append no mesmo proprietário, evitando que o refresh sobrescreva uma página recém-adicionada.
- Preservar `metadataPins`, sessões abertas por ID exato, arquivados, grupos recolhidos, LEDs, seleção, drag/reorder e ordem manual de tópicos.
- Não declarar `No sessions` nem oferecer a ação de tópico vazio com base apenas numa página parcial. Usar metadados autoritativos de associação/contagem já disponíveis; se faltarem, manter estado incompleto até poder decidir, sem baixar todos os históricos.
- Remover a injeção dos quatro botões em `App.tsx`; passar estado/callbacks de paginação à sidebar ou sentinels com root explícito.

## 5. Demais superfícies — inventário de implementação

Todos os caminhos abaixo são relativos a `clients/desktop/src`.

| Superfície e arquivos | Comportamento a implementar/preservar |
| --- | --- |
| Happening now: `App.tsx`, `features/klm-agent/activityPaging.ts`, `KlmAgentWorkspace.tsx` | Página inicial 25 e append de metadados. Trocar `older/latest/next` por estado de paginação com loading/error/retry. Refresh reconcilia toda a faixa carregada e remove sessões realmente inelegíveis, sem expulsar páginas só porque saiu da primeira. Respeitar ordenação manual. A lista recolhida mantém três itens e Show more/Show less; autopaging ocorre ao expandir ou quando faltam itens visíveis, e no scroll próprio do widget Focus. |
| Feed: `features/klm-agent/KlmAgentWorkspace.tsx`, `useGithubActivityFeed.ts` | Sentinel inferior no scroller efetivo, que muda conforme o layout. Limite 25. Preservar reconstrução contígua, TTL, busca/filtros, bookmarks, âncora e aviso de novos posts; página antiga carregada não anuncia novidade. |
| Atividade agrupada: `features/klm-agent/GithubActivityCard.tsx`, `usePipelineMembers.ts` | Trocar 20 por 25; autopaging só no leitor aberto. Preservar watermark capturado e Update explícito para admitir transições novas. Paginar não equivale a aceitar updates. Manter estados de Details, retenção e identidades de membros. |
| Grafos: `features/graphs/GraphRunActivity.tsx`, tipos em `engine.ts` | Eventos com topo/fundo e âncora, primeira página na cauda, sem reset para latest ao paginar. Lista de ativações acumulada em lotes de 25; seleção da ativação independente da página. Polling não deve substituir a faixa por uma página nem continuar retry de falha de paginação sem controle. |
| Tasks: `features/activity-board/ActivityBoardPage.tsx`, `api.ts` | Preservar observer horizontal de montagem da coluna; adicionar paginação vertical em `.board-column-items` ou no scroller real. Limite 25. Identidade inclui projeto, fase, sprint e revisão de projeção. Dedup por `item.key`; recarregar faixa carregada após atualização, sem perder posição nem alterar filtros. |
| Webhooks: `features/webhooks/WebhooksSettings.tsx`, `api.ts` | Sentinel inferior, 25 por request, append por ID. Substituir o refresh com `loadedLimit` de até 100 por revalidação paginada da faixa carregada. Manter pausa durante edição/mutações e retorno de foco. |
| Automações: `features/automations/AutomationsSettings.tsx`, `api.ts` | Converter tanto a lista quanto histórico. Append imediato da próxima página; polling revalida a faixa carregada. Dedup do histórico por `runId`, não `sessionId`, pois sessões podem ser reutilizadas. Usar `nextOffset` do servidor. Preservar seleção do detalhe/editor. |
| Tópicos e destinos: `features/workspace/useTopicPage.ts`, `TopicDialog.tsx`, `features/automations/AutomationEditor.tsx` | Lista acumulada por consulta, 25, erro/retry, seleção mantida independentemente da página. Sentinel dentro do picker aberto. Destino de automação escolhido não muda ao filtrar ou carregar mais. |
| Agentes: `features/agents/useAgentPage.ts`, `api.ts`, `AgentsPage.tsx`, `AgentPicker.tsx`, `AgentEditorDialog.tsx` | Lista e referências acumuladas, 25, busca por escopo, geração de engine preservada e refresh da faixa. Chave estável de agente por identidade/source, sem índice de página. Picker recolhido não pagina. Ler uma página nunca seleciona um agente. |
| Library: `features/library/LibrarySkillsPaging.tsx`, `api.ts`, `LibraryPanel.tsx` e chamadores do paging | Skills e referências de remoção acumuladas, 25, sentinel na seção aberta. Preservar seleções fora da consulta e revisão de confirmação de remoção. Atualizar somente a coleção paginada, não sobrescrever conexões/MCP com um snapshot parcial de skills. |
| Filas e solicitações: `features/chat/MessageQueue.tsx`, `PendingRequestPages.tsx`, `DelegationRequests.tsx`, `GraphRequestPages.tsx` | Substituir More/First por append automático de 25 no scroller correspondente. Revalidar/remove resolvidos após updates; dedup por identidade composta quando há vários donos/tipos. Resposta/remover/Send now continuam ações explícitas nos IDs corretos. Cursor removido exige recarga controlada, sem reviver cartões resolvidos. |

### Pickers baseados em select nativo

Não observar o fundo do formulário para carregar todas as opções de um `<select>`: o scroll do popup nativo não é exposto de modo confiável.

Para seletores paginados de ativações, sessões e tópicos, reutilizar `design-system/SearchSelect.tsx`, acrescentando somente suporte apresentacional a lista controlada: busca externa opcional, callback de aproximação do fim, loading/error/footer e estado de abertura. O fetch, identidade e merge ficam nas features. Sem modo remoto, preservar o filtro local existente. Manter teclado, opção selecionada, fechamento, disabled e validação obrigatória dos formulários.

### Limites desta conversão

- Controles de expansão como Show more/Show less, abertura de Details e seleção de Latest activation são ações de apresentação/seleção e continuam disponíveis.
- `EventContent.tsx` pagina **bytes** de um único conteúdo, não uma lista de itens. Preservar o contrato de chunks, UTF-8 e leitura explícita; o lote 25 não se aplica a bytes. O mesmo vale para downloads e payloads grandes.
- `getLibrary` atualmente completa o inventário MCP, limitado a 64, para os seletores. Manter essa finalidade e seleção completa; ajustar o tamanho de cada request sem fazer uma página parcial ser interpretada como conexão removida. A paginação visual de skills deve permanecer independente desse inventário.
- Não alterar controles de período do Analytics nem limites de membros de batches de subagentes, que têm semântica própria.

## 6. Critérios de aceitação e validação humana

Validar com dados existentes ou ambiente DEV isolado, sem instalar/reiniciar produção como parte desta correção.

| Cenário | Resultado esperado |
| --- | --- |
| Primeira carga e páginas seguintes | Network mostra `limit=25`/`eventLimit=25` nos endpoints abrangidos; fim parcial não provoca request adicional após indicação de fim. |
| Várias entradas na borda e React Strict Mode | Uma carga por lista/cursor em voo; sem duplicação de itens, sem cursor consumido duas vezes. |
| Viewport alto ou página com poucas linhas visíveis | Carga sequencial até preencher, fim ou limite seguro da janela; sem drenar históricos ocultos nem ping-pong de bordas. |
| Chat com mais de 200 eventos | Subir várias páginas, cruzar o recorte e descer novamente mantém continuidade e posição; não exige Latest messages. |
| Prepend durante leitura e streaming simultâneo | A linha lida mantém posição; não pula para cauda; o fim real continua acompanhando quando já estava sendo acompanhado. |
| Histórico com ferramentas agrupadas, subagentes e conteúdo grande | Projeção compacta preservada; referências de conteúdo grande seguem sob demanda; nenhum histórico de filho fechado no bootstrap. |
| Trocar rapidamente sessão/projeto/busca/sprint/ativação/engine | A resposta atrasada não altera a nova lista, seleção, cursor ou loading. |
| Falhar uma página e permanecer perto da borda | Um erro recuperável, sem loop de requests; Retry retoma o cursor apropriado. |
| Sessões/tópicos além de quatro páginas | Itens anteriores continuam acessíveis; seleção, arquivados, busca, grupos e ordenação permanecem corretos. |
| Inserção/remoção enquanto páginas antigas estão abertas | Refresh preserva a fronteira/âncora, incorpora novidades e remove itens realmente excluídos sem descartar a faixa por truncamento. |
| Listas com itens resolvidos ou sessão reutilizada | Solicitações resolvidas não reaparecem; execuções distintas da mesma sessão não são deduplicadas entre si. |
| Superfícies fechadas, minimizadas ou recolhidas | Não iniciam paginação por sentinels ocultos. Reabrir permite continuar corretamente. |
| Desktop, Focus e viewport mobile/WebView | Observer usa o contêiner correto; toque e teclado conseguem alcançar todas as páginas; não há foco roubado. |
| Novos posts e leitores de atividade agrupada | Aviso de novos posts e Update preservam sua semântica; carregar página antiga não muda watermark nem marca novidades. |

Para o rail, incluir tópicos inicialmente recolhidos, arquivados e uma busca cujo resultado útil esteja depois da primeira página. Para scroll, incluir leitura enquanto a requisição ainda está pendente e duas visualizações da mesma conversa.

## 7. Checagem leve e entrega

1. Durante a implementação, adaptar apenas fixtures/assertions existentes diretamente invalidados pela mudança, como os cliques em `Load more` em `GithubActivityCard.review.test.tsx` e o contrato direcional de `sessionState.test.ts`. Não criar suíte de integração ou review adversarial.
2. Ao concluir as alterações TypeScript, executar uma checagem: `npx tsc -b` em `clients/desktop`. Não somar build Vite e suíte completa por padrão.
3. Como há handlers Go fixos a parametrizar, compilar uma vez o engine dev, com saída no diretório temporário autorizado: `go build -tags dev -o "C:\Users\Samuel\AppData\Local\Temp\opencode\klm-pagination-check.exe" .`, em `engine`, após conferir o diretório pai. Isso verifica compilação, não comportamento.
4. Registrar o que foi implementado, as checagens realmente executadas e os cenários humanos ainda pendentes. Uma checagem falha por mudanças locais preexistentes deve ser reportada com o erro concreto, sem sobrescrever esses trabalhos.

**Estado deste documento:** plano baseado na leitura dos arquivos atuais e no contexto fornecido. Nenhuma correção de código foi implementada, nenhum build/teste foi executado e nenhuma validação de runtime foi realizada nesta tarefa de planejamento.
