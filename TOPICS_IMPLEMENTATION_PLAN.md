# Topics — plano de implementação

## Ajustes aprovados após a entrega inicial

O usuário autorizou posteriormente reordenar tópicos e mover sessões entre tópicos
por drag-and-drop. Essa decisão substitui as restrições iniciais abaixo quanto a
essas duas operações de UI. A implementação atual persiste a ordem manual no projeto,
mantém sessões em ordem cronológica dentro do tópico e permite destinos ativos já
existentes no mesmo projeto. A autoridade dos agentes continua limitada à definição
inicial; não há ferramentas de reordenação ou reassociação para agentes. Ver
`TOPICS_IMPLEMENTATION_PROGRESS.md` e `product.md` para o comportamento entregue.

## Objetivo e autorização

Substituir as pastas visuais de sessões por tópicos persistentes, identificados uma única vez pelo agente e herdados automaticamente por sessões derivadas. Manter relacionadas as conversas de um mesmo trabalho mesmo depois de Stop, dispose do runtime, reinício ou arquivamento.

O usuário solicitou este plano e sua implementação por um agente em uma nova sessão OpenCode, GPT-6.1 Sol, esforço High. Não há autorização para executar graphs, criar outras sessões, fazer commits/push ou realizar revisão adversarial.

Referências: `AGENTS.md`, `product.md` e imagem `C:/Users/Samuel/Downloads/topics.png`. Ler a imagem antes de implementar a UI. Este documento é o plano da mudança; o produto atual ainda descreve pastas, e sua documentação deve ser atualizada com o que efetivamente for entregue.

## 1. Contrato funcional

### Decisões aprovadas

- Uma nova sessão independente começa sem tópico. Assim que identificar o objetivo, inclusive em conversas de esclarecimento, o agente registra um nome curto e descritivo.
- O agente define o tópico uma vez. Depois, somente o usuário pela interface pode renomeá-lo; o agente não troca nem renomeia tópicos existentes.
- Sessões spawnadas herdam o mesmo tópico pela engine, inclusive nas gerações seguintes; não depender de o agente copiar um nome.
- O tópico tem identidade própria, independente do título das sessões e de seu nome editável. Não unir tópicos automaticamente por semelhança ou igualdade de nomes.
- Na lateral, as sessões do mesmo tópico formam um bloco contínuo, delimitado por contorno, com o nome no cabeçalho. Clicar no cabeçalho recolhe/expande; recolhido, apenas o cabeçalho permanece.
- Na navegação do chat, à direita, a tag do tópico substitui o seletor de pasta. Clicar abre edição de nome e cor para o tópico inteiro.
- A primeira versão inclui seleção de cores.
- Não repetir a tag em cada linha da lateral: cabeçalho e contorno já identificam o grupo.

### Defaults propostos nesta conversa, adotados para a primeira entrega

- Sessões mais recentes primeiro dentro do tópico, por `createdAt`, com desempate estável por ID. Atividade, mensagens e dispose não reordenam sessões.
- Tópicos ordenados pela sessão mais recentemente criada no grupo; desempate estável. Usar os membros persistidos, não apenas os atualmente visíveis, para evitar saltos por arquivamento.
- Paleta pequena predefinida com variantes de contraste para os temas; cor inicial neutra, sem atribuição aleatória e sem seletor RGB livre.
- Cada pasta legada vira um tópico, preservando associação e arquivamento. `Ungrouped` continua como ausência de tópico, não como tópico real compartilhado.
- Sem mover sessões entre tópicos, drag-and-drop de ordenação ou criação manual de grupos nesta versão. A regra de associação é definição inicial e herança; edição muda nome/cor, não associação.
- `New session` global cria sessão sem tópico, independentemente do tópico selecionado. Remover os antigos controles de criação de pasta e criação em pasta; não introduzir fluxos extras.
- Estado recolhido é local ao cliente, por engine/projeto/ID do tópico, como o recolhimento de pastas atual.

## 2. Mapa do código atual

- `engine/store.go`: `Project.Folders`, `Project.ArchivedFolders`, `Session.Workspace`, `diskState`, carga/normalização.
- `engine/api.go`: rotas `/api/projects/{id}/folders`, criação e edição de sessões, validação `workspace`, helper `folderArchived`.
- `engine/session_collaboration.go`: `newTopLevelSession`, `spawnSelectionLocked`, `spawnSessionLocked`, recibos duráveis `SessionSpawn`, `conversationArchived`.
- `engine/session_spawn.go`: catálogo/opções de spawn e revalidação de configuração após buscar modelos fora do lock.
- `engine/linked_bridge.go`: catálogo privado e dispatch das ferramentas de conversação.
- `engine/graph_prompts.go`, `engine/prompts/session-collaboration.md`: instruções de colaboração; localizar montagem do contexto de cada turno nos três adapters.
- `engine/general_agent_tools.go`, `engine/prompts/general-agent.md`: controles globais existentes de pasta e criação de sessões; adaptar sem ampliar autoridade.
- `engine/transactions.go`, `engine/stream_journal.go` e serialização/projeções relacionadas: persistência transacional e atualizações incrementais. Verificar campos novos em clone, replay, checkpoint e resumos.
- `clients/desktop/src/engine.ts`: contratos compartilhados do frontend.
- `clients/desktop/src/App.tsx`: criação, seleção, recolhimento, API de pastas, filtros de disponibilidade e seletor na navegação do chat.
- `clients/desktop/src/features/workspace/WorkspaceSidebar.tsx`: grupos, menus, busca, arquivo, drag-and-drop e estados de runtime.
- `clients/desktop/src/features/workspace/CreateSessionDialog.tsx`, `FocusSessionChat.tsx`, `FocusWorkspace.tsx`: criação e apresentações compartilhadas.
- `clients/desktop/src/features/klm-agent/KlmAgentWorkspace.tsx`, `features/projects/ProjectRail.tsx`, `features/chat/SessionSpawnEvent.tsx`: disponibilidade, abertura de sessões e estados agregados.
- `clients/desktop/src/styles.css`, `features/workspace/mobile-workspace.css`, `focus-workspace.css` e design-system: estilos/tokens.

Não renomear indiscriminadamente `workspace`: diretórios/worktrees de graphs são outro conceito e permanecem intactos.

## 3. Engine: modelo e migração

1. Introduzir um registro de tópico por projeto com `id`, `name`, `color` (chave da paleta), `createdAt` e estado de arquivamento. Uma coleção em `Project` é suficiente se compatível com as transações existentes; não criar armazenamento paralelo desnecessário.
2. Adicionar `topicId` às sessões normais. ID vazio significa sem tópico. Validar que o tópico existe no mesmo projeto; nome não é chave estrangeira.
3. Separar helpers de disponibilidade e agrupamento das antigas strings de pasta. Preservar tratamento de projeto removido, sessão arquivada e tópico arquivado.
4. Implementar migração versionada, idempotente e durável no caminho normal de carga/persistência. Cada pasta legada vira um tópico do respectivo projeto; sessões conservam seus IDs, eventos, runtime/native metadata, configurações, permissões, filas e recibos.
5. Não gerar pastas padrão Active/Closed/Evaluate para novos projetos. Em dados legados, preservar grupos existentes, inclusive arquivados, sem inferir novos assuntos lendo histórico.
6. Manter os campos históricos necessários para ler recibos e snapshots antigos. Não reescrever requisições históricas de spawn, cujo conteúdo participa da idempotência.
7. Alterações devem usar lock e `commitLocked`, com rollback normal em falhas. Integrar novos campos às transações, clone/checkpoint/replay e projeções HTTP/SSE; não considerar apenas a gravação do JSON principal.
8. Migração não deve iniciar harnesses, executar agentes ou tocar no diretório de trabalho. Não editar dados instalados do usuário manualmente para testar.

## 4. Definição inicial pelo agente

Adicionar uma ferramenta privada, por exemplo `session_topic_set`, com apenas o nome inicial do tópico como entrada e retorno do tópico efetivo.

- A identidade da sessão/projeto vem do contexto autenticado, nunca de um ID arbitrário fornecido pelo modelo.
- Disponível para a sessão normal principal de projeto; não para general-agent, graph nodes ou subagents. Side chat lê o tópico da principal, não redefine sua associação.
- No lock, validar turno ativo, papel, projeto e ausência de tópico. Criar e associar numa única transação.
- Se a sessão já possui tópico, retornar o tópico atual sem mutação (inclusive depois de rename humano). Chamadas concorrentes/repetidas não criam tópicos extras nem desfazem nome/cor escolhidos pelo usuário.
- Validar nome como label UTF-8 curta, aparada e não vazia, usando convenções existentes. Usar cor neutra determinada pela engine.
- Entregar contexto de tópico e orientação de definição inicial a Pi, OpenCode e Codex pelos caminhos internos existentes. Uma sessão com tópico herdado não deve receber instrução para criar outro.
- Não iniciar turnos adicionais para classificar. O agente define durante o turno normal assim que o objetivo estiver claro.
- Adicionar a ferramenta ao catálogo, parsing/dispatch e transporte dos três harnesses; respeitar separação `klm_linked`/`klm_library`.
- Bibliotecas, recursos MCP e resultados externos não fornecem autorização para controle de sessão. Esta ferramenta é metadado restrito à própria conversa; não deve abrir acesso a outras mutações.

## 5. Spawn e demais ferramentas

- Copiar `topicId` da sessão de origem na transação de criação, antes do primeiro turno da filha. Descendentes herdam o mesmo ID.
- Para spawn a partir de side chat, resolver o tópico da principal; não usar um campo independente e possivelmente desatualizado da side.
- Se uma origem normal ainda não tiver tópico, retornar erro recuperável antes da criação, orientando a principal a definir o tópico primeiro. Para side sem tópico na principal, orientar definição na principal sem conceder à side poder de edição. Não criar filhas órfãs nem inventar nome pelo título.
- General-agent continua sem tópico próprio. Sessões que ele cria em um projeto começam sem tópico e o definem no primeiro turno normal.
- Preservar `sourceUserEventId`, recibos duráveis, autoria de prompts, herança de harness/model/effort/YOLO e seleções Library. Tópico não autoriza spawn, consulta ou execução de graph.
- Revalidar o tópico efetivo após operações fora do lock, assim como hoje se revalidam configurações de spawn. Um retry de criação aceita deve devolver o mesmo recibo/filha.
- Remover `workspace` como opção de agrupamento dos catálogos/instruções novos; não permitir que parâmetros legados contornem a herança. Preservar leitura e comparação dos recibos antigos; rejeitar novos overrides incompatíveis de modo claro.
- Expor nome/ID do tópico nas projeções de descoberta/detalhes quando útil para distinguir sessões, sem ampliar regras de consulta.
- Adaptar os controles de pasta do general-agent: ele não pode renomear tópicos nem mover sessões para contornar a regra de edição humana. Preservar controles de sessão não relacionados.

## 6. API e arquivamento

- Expor tópicos no inventário do projeto e `topicId` nos resumos/detalhes de sessão, com atualizações SSE pelos canais existentes.
- Adicionar PATCH de tópico com identidade de projeto+tópico, validação de nome e chave de cor e resposta canônica. A UI pode alterar nome/cor juntos atomicamente.
- Não expor rename/cor como ferramentas de agente. A API conserva o contrato de rede existente do produto; não alegar autenticação humana nova.
- Preservar arquivo/restauração de sessões e grupos, agora tópicos, como continuidade do comportamento atual. Arquivar é metadado, não Stop.
- Restauração de sessão sob tópico arquivado não pode movê-la implicitamente para outro tópico. Usar restauração explícita do tópico, com consequência clara na UI, ou pedir que o usuário restaure o tópico primeiro.
- Todas as superfícies devem usar a disponibilidade por tópico: rail, Happening now, Focus, overlays, cards de spawn e consultas. Sessões arquivadas continuam legíveis nos caminhos atuais, mas não ficam disponíveis para execução sem restauração.
- Retirar os fluxos de pasta da UI e impedir mutações legadas que desalinhem associação nova; não deixar duas fontes de verdade concorrentes.

## 7. Interface

1. Criar helpers pequenos compartilhados para resolver tópico e ordenar grupos/sessões; não duplicar regras entre sidebar e navegação.
2. Substituir grupos de pasta pelo bloco da imagem: contorno fino, cabeçalho com nome e indicação discreta de expandir/recolher, linhas internas mantendo seleção, LEDs e menus existentes.
3. Renderizar sessões sem tópico em seção neutra `Ungrouped`. Sessões novas devem estar visíveis imediatamente antes da classificação.
4. Remover reordenação manual e drag-and-drop entre grupos para não contrariar agrupamento estável e herança. Não alterar a ordenação manual própria de Happening now.
5. Manter busca por título e incluir nome do tópico. Durante busca mostrar grupos necessários sem sobrescrever recolhimento salvo.
6. Substituir `Move session to folder` em `App.tsx` pela tag clicável. Sem tópico, não inventar um nome nem oferecer seletor de pasta.
7. Reutilizar primitivas de diálogo/menu/input/button para edição `Topic`, `Name`, `Color`, `Save`, `Cancel`. Salvar com estado pendente, erro recuperável e atualização de todas as referências pelo mesmo ID. Cancelar não altera nada.
8. Definir cores por tokens semânticos/variantes do tema. Aplicar somente a tag, cabeçalho e contorno; manter fundo das sessões e cores dos LEDs. Nome continua identificando o grupo sem depender de percepção de cor.
9. Suportar teclado/foco, `aria-expanded`, labels de cores em inglês, contraste e nomes longos com truncamento e acesso ao texto completo.
10. Atualizar navegação para abrir/expandir pelo ID de tópico. Migrar estado local de recolhimento por nome quando inequívoco, ou usar novo namespace local sem afetar dados persistidos.
11. Desktop, browser Focus e WebView mobile reutilizam a mesma lógica. Preservar botões de janela flutuante, drafts, status mobile, histórico, composer e controles de graph. Não alterar telas nativas Flutter sem necessidade.

## 8. Ordem de execução

1. Ler instruções, imagem e estado atual do checkout; preservar mudanças existentes.
2. Implementar tipos, migração e helpers de disponibilidade/persistência.
3. Implementar ferramenta inicial e contexto dos três harnesses, depois herança de spawn e adaptação dos catálogos.
4. Implementar API, inventário e arquivamento por tópico.
5. Atualizar contratos TypeScript, agrupamento lateral, tag e edição de nome/cor.
6. Ajustar superfícies dependentes e remover controles/instruções antigas conflitantes.
7. Atualizar `product.md`, documentação relevante e criar `TOPICS_IMPLEMENTATION_PROGRESS.md` com entrega, checks reais e pendências de validação humana.

## 9. Validação mínima e aceitação humana

Seguir `AGENTS.md`: sem suites completas, revisão adversarial, agentes revisores ou testes de integração/complexos. Para mudanças Go+TS, fazer um build Go dev e um typecheck TypeScript focado (usar scripts reais do projeto). Não executar vários checks equivalentes nem produzir instaladores. Ajustar fixtures/types existentes apenas quando necessário para manter compilação.

Checklist para validação humana posterior, não alegar execução sem fazê-la:

- Sessão sem tópico registra um tópico durante conversa normal nos três harnesses.
- Nova chamada da ferramenta não renomeia nem duplica; rename humano permanece após novo turno/restart.
- Spawn, spawn de descendente e spawn via side herdam exatamente o mesmo ID; general-agent não mistura trabalhos diferentes.
- Nova sessão global não herda acidentalmente o tópico visualmente selecionado.
- Ordem por criação e grupos contínuos sobrevivem a mensagens, Stop, dispose e restart.
- Nome/cor atualizam cabeçalho, tag e todas as sessões do grupo; cancelar/erro não aplica mudanças indevidas.
- Colapso persiste localmente, busca e abertura por cards funcionam.
- Migração conserva sessões, recibos e arquivos; pastas arquivadas não reaparecem como conteúdo ativo.
- Arquivamento/restauração mantém identidade e disponibilidade coerentes em rail, Happening now, Focus e mobile.
- Cores e seleção legíveis em dark/light, navegação por teclado e touch preservadas.

## 10. Limites e cuidado com o checkout

Há trabalho local não commitado em `App.tsx`, `WorkspaceSidebar.tsx`, arquivos do KLM Agent/activity feed, `engine/api.go`, `store.go`, adapters, ferramentas do general-agent, `stream_journal.go`, `product.md` e outros. Ler versões atuais e aplicar alterações pontuais; não sobrescrever, resetar ou limpar esse trabalho. Não modificar `code.html`.

Fora do escopo: classificação retroativa por LLM, merge automático de tópicos, múltiplos tópicos por sessão, mudança automática de assunto, edição de associações pelo agente, cores livres, novos recursos de graph, monitoramento de outras sessões e redesign geral.
