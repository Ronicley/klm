# Review — agrupamento de workflows/deployments

## Estado final das revisões — 2026-10-16

**Duas rodadas de review/correção encerradas pelo coordenador.** As pendências históricas abaixo foram superadas pela conferência específica de R2-1 descrita aqui. Ativação do feed e validação visual com o usuário continuam pendentes.

- Conferidos `retainedActivity`/revalidação no `useGithubActivityFeed.ts`, tratamento de `retentionPending` no card e os cenários adicionais do arquivo de testes.
- A menor expiração passa a limpar os snippets vencidos e disparar revalidação, mantendo identidade e leitores; resposta vazia remove o grupo. Recuperação usa o erro/Retry existente.
- Executado pelo coordenador: `npx vitest run src/features/klm-agent/GithubActivityCard.review.test.tsx` → **1 arquivo, 7 testes passaram** (4.98s). Inclui o teste originalmente reprovado, preservado, além de card montado/página 2/Details, remoção final e falha/recuperação.
- Não repetidos testes Go, pois esta correção não alterou o backend. Sucesso anterior do comando Go focado está registrado na rodada 2. TypeScript desta entrega foi reportado pelo implementador; não foi reexecutado pelo coordenador nesta conferência específica.
- Nenhum teste visual real de geometria/scroll, alteração de estado vivo, troca de binário ou reinício foi realizado. A aprovação fecha os achados de código combinados, não declara ativação nem aceite humano.

## Rodada 1 — 2026-10-16

Status: revisão realizada; patches dos três achados implementados pela sessão implementadora em 2026-10-16. Checks focados registrados abaixo; conferência dos patches pelo coordenador e rodada 2 permanecem pendentes. Ativação do feed em uso continua pendente.

Entrega identificada na sessão ativa `Implementação feed group` (`b7b00428fd28eef46e58bd0b70025f89`). A sessão original `adcb15b229f7abb87af81c3eb589bba4` está arquivada e só registra falhas de inicialização. A resposta final da sessão ativa corresponde ao código e à documentação entregues.

Escopo inspecionado: extração/correlação/projeção em `github_activity_groups.go`, paginação e Details em `github_activity_api.go`, normalização, contrato do agente, DTOs, card/hook do feed, bookmarks e roteiro de aplicação aos posts existentes. Alterações de harness interativo são trabalho alheio e não fazem parte desta revisão.

Verificação executada pelo coordenador:

```text
go test -tags dev -run '^(TestGithubActivityGroupingIdentityAndState|TestGithubActivityGroupingPaginationAndMembers|TestGeneralFeedRead)$' .
```

Resultado: `ok klm/engine 1.085s`. São os testes focados existentes, não uma suíte completa. Não foi executada validação visual nesta rodada. Os achados abaixo são demonstráveis pelo fluxo de estado/ciclo de vida do código; não alegam reprodução no navegador em uso.

### R1-1 — P2 — Cada transição descarta a leitura do histórico expandido

Local: `clients/desktop/src/features/klm-agent/GithubActivityCard.tsx:57–75,100,114,118`.

`sourceKey` inclui `post.releaseSequence`; qualquer novo evento do grupo remonta `PipelineMembers`. O componente então perde `items`, `cursor` e o watermark fixado em `useState`, voltando à primeira página. Isso ocorre também na lista completa de jobs/deployments. Em um pipeline ativo, o poll pode fazer o usuário perder repetidamente as páginas que abriu; manter a âncora do article não preserva a posição dentro do histórico encolhido.

Cenário: abrir Activity de um grupo com >20 membros, carregar a segunda página e receber uma nova transição da mesma run. O remount descarta a segunda página e muda a altura do card.

Correção solicitada: preservar o componente e a janela de leitura/paginação em atualizações normais. Reconciliar novidades sem reset automático das páginas carregadas, usando snapshot coerente e atualização explícita quando necessário. Aplicar também à lista completa de filhos. Preservar a posição interna de leitura; não basta manter a identidade do article.

### R1-2 — P1 — Histórico aberto pode continuar exibindo membros expirados

Local: `GithubActivityCard.tsx:57–75,100,114,118`; `useGithubActivityFeed.ts:167–174`.

`PipelineMembers` guarda os eventos carregados sem verificar seus `expiresAt` nem reconciliá-los com a população atual do grupo. Sua invalidação depende da última release visível. A expiração de um membro antigo pode reduzir `eventCount` e alterar `expiresAt` do card sem mudar `post.id`, `releaseSequence`, `query` ou watermark. Nesse caso o histórico continua mostrando o membro antigo e eventuais Details já carregados.

Cenário determinístico: Activity aberto, membro antigo com expiração T e membro recente com expiração posterior. Um refresh chega logo após T, antes do próximo tick do relógio local: o card é substituído por outro com a mesma identidade/release, contagem menor e nova expiração futura. O hook não remove o card; `PipelineMembers` não remonta nem refaz a consulta e mantém o membro expirado. Também há risco com remoções que não mudem a release mais recente.

Correção solicitada: aplicar retenção aos membros/filhos armazenados no estado React e invalidar/reconciliar páginas e Details quando a composição retida mudar, mesmo sem nova release. Não resolver só adicionando `eventCount` à key, pois isso mantém o problema de perda de leitura de R1-1. Usar identificação de composição/revisão ou estratégia equivalente simples, respeitando os limites/snapshot e sem manter conteúdo expirado.

### R1-3 — P2 — Bookmark se perde quando chega a evidência de correlação

Local: `KlmAgentWorkspace.tsx:162,220`; `github_activity_groups.go:268–285,348`; limitação reconhecida em `GITHUB_ACTIVITY_GROUPING_ROLLOUT.md:46`.

Bookmarks são apenas `Set<post.id>`. Um deployment pode inicialmente aparecer como `pipeline-<deployment>` e depois ser incorporado a `pipeline-<run>` quando chegar o job/check que fornece a relação; um check individual também pode depois ser absorvido. O item antigo some da listagem, mas o bookmark fica preso ao ID antigo. Isso pode ocorrer em operação normal por ordem de entrega, sem reload, migração ou expiração.

Correção solicitada: preservar a intenção do bookmark durante a passagem individual/deployment → grupo, com referência aos eventos originais/alias de identidade ou solução equivalente. Não exigir carregar todo o histórico no browser nem tornar bookmarks persistentes. Para expiração/remoção real, manter o contrato de retenção. Ajustar a documentação, que hoje aceita uma perda contrária ao plano de preservação das interações.

### Saída esperada das correções

- Corrigir os três achados com patches focados, preservando outras alterações locais.
- Registrar comportamento escolhido, arquivos e evidências por achado. Se discordar de algum ponto, demonstrar tecnicamente o cenário e a regra de produto que o sustenta.
- Fazer a menor verificação pertinente; cobrir os cenários de atualização/expiração/correlação tardia, sem suíte completa nem integração complexa.
- Não ativar a versão/migração, reiniciar engine ou trocar binários. O coordenador conferirá as correções na segunda rodada quando o usuário sinalizar a entrega.

## Rodada 2 — 2026-10-16

Status: conferência do coordenador realizada após o usuário informar que as correções terminaram. Patch do achado residual R2-1 entregue pela sessão implementadora; aguarda conferência específica do coordenador antes da ativação. Isso não registra aprovação final.

### Conferência dos patches da rodada 1

- R1-1: confirmado que transições normais não remontam mais `PipelineMembers`; a janela mantém watermark/profundidade e há atualização explícita.
- R1-2: confirmado hash de composição e remoção/revalidação dos membros no leitor. A proteção funciona no componente isolado, mas o ciclo de expiração do feed pai ainda desmonta todo o card (R2-1 abaixo).
- R1-3: confirmado resolver limitado por eventos originais e watermark; a seleção acompanha correlação tardia sem carregar o histórico inteiro. O endpoint usa as fontes elegíveis e omite origens expiradas/removidas.

Verificações do coordenador antes do teste adicional:

- `npx vitest run src/features/klm-agent/GithubActivityCard.review.test.tsx`: quatro testes React/jsdom passaram.
- `go test -tags dev -run '^(TestGithubActivityGroupingIdentityAndState|TestGithubActivityGroupingPaginationAndMembers|TestGithubActivityGroupingRetentionAndBookmarks|TestGeneralFeedRead)$' .`: sucesso, resultado retornado pelo cache Go (`ok klm/engine (cached)`).
- Não houve suíte completa, validação visual ou ativação do engine.

### R2-1 — P2 — Feed pai remove um grupo inteiro na expiração de apenas um membro

Locais: `engine/github_activity_groups.go:357–361`; `clients/desktop/src/features/klm-agent/useGithubActivityFeed.ts:167–174`; renderização de cards em `KlmAgentWorkspace.tsx`.

O backend define `card.expiresAt` como a menor expiração de seus membros. O hook do feed trata esse valor como expiração definitiva do card e o remove do snapshot/listagem; só o próximo poll normal (até 15 segundos) o reconstrói com os membros ainda retidos. Isso desmonta `GithubActivityCard` e seu leitor, perdendo Activity aberta, páginas e Details dos membros sobreviventes, apesar da correção em `usePipelineMembers`.

O teste de R1-2 passa porque monta diretamente `GithubActivityCard` e mantém o card montado ao avançar o relógio; ele não passa pelo filtro de TTL do feed pai.

Reprodução automatizada adicionada pelo coordenador em `GithubActivityCard.review.test.tsx`: `revalidates a group at member expiry without dropping its surviving card from the feed`. Usa o hook real `useGithubActivityFeed`, API mock e relógio controlado. A API entrega um grupo cuja menor expiração ocorre em um segundo, e já oferece a versão reconstituída com 20 membros sobreviventes após esse instante. Ao avançar dois segundos, o hook entrega lista vazia, em vez do mesmo grupo revalidado.

Comando executado:

```text
npx vitest run src/features/klm-agent/GithubActivityCard.review.test.tsx -t 'revalidates a group at member expiry'
```

Resultado: **1 failed, 4 skipped**; `expected [] to deeply equal ['run-card']`. O teste está deliberadamente presente como regressão a corrigir, não é reportado como aprovado.

Correção solicitada:

- Distinguir a necessidade de revalidar composição de um grupo da expiração definitiva de um post individual.
- Revalidar o grupo prontamente quando vence um membro; preservar sua identidade montada/janela aberta enquanto houver conteúdo retido. Não esperar silenciosamente o poll de 15 segundos para reaparecer.
- Não conservar dados expirados para evitar o remount: ocultar/remover snippets/membros expirados e manter apenas uma estrutura transitória sem conteúdo vencido, se necessário, até a revalidação.
- Remover de fato o card quando nenhum membro elegível sobreviver. Falha de revalidação deve permitir recuperação sem apresentar conteúdo vencido.
- Ampliar o teste existente somente o necessário para comprovar também preservação de Activity/páginas dos sobreviventes e remoção final. Manter checks focados; não ativar o feed vivo.

A conferência final desta correção fecha a pendência da rodada 2; não equivale a uma nova rodada ampla de review. Migração/ativação permanece pendente.

### Correção R2-1 entregue — 2026-10-16

- `useGithubActivityFeed.ts`: `expiresAt` de grupo passa a disparar revalidação imediata da janela carregada, enquanto posts individuais continuam sendo removidos no próprio TTL. Se já houver request em andamento, o refresh é enfileirado pelo mecanismo existente.
- Tanto o render quanto o snapshot local descartam texto, resultado, links e timestamps do representante antigo no vencimento. Um shell transitório `retentionPending` mantém apenas a identidade do grupo e previews ainda retidos. A resposta da API atualiza o mesmo card; ausência de sobreviventes remove-o de fato. Erro mantém a estrutura sem snippets vencidos e oferece o caminho normal de Retry do feed.
- `GithubActivityCard.tsx` e DTO em `githubActivityApi.ts`: suportam esse shell sem desmontar os leitores; contagens antigas não são exibidas e não é possível criar bookmark com uma referência provisória. Activity/páginas/Details de membros sobreviventes mantêm identidade; cada leitor continua descartando seus próprios membros vencidos.
- O teste original do coordenador foi preservado, inclusive as asserções `['run-card']` e `eventCount=20`, e passou. Dois cenários adicionais no mesmo arquivo exercitam o hook pai com cards montados: (1) Activity aberta, página 2 e Details sobrevivem à expiração parcial, mantendo os mesmos nós, e depois o grupo é removido quando termina a retenção; (2) request pendente/falho não expõe cabeçalho/summary/preview/membro vencidos, preserva Details ainda válidos e recupera via Retry sem remount do card.
- Checks executados: `npx vitest run src/features/klm-agent/GithubActivityCard.review.test.tsx` → **um arquivo, sete testes passaram**; `npx tsc -b --pretty false` → **passou**. São checks React/jsdom e de tipos, não validação visual. Backend Go não foi alterado nesta correção e seus testes não foram repetidos.
- `git diff --check` limitado aos três arquivos de implementação desta correção passou; apenas avisos LF/CRLF.
- Arquivos desta correção: `useGithubActivityFeed.ts`, `GithubActivityCard.tsx`, `githubActivityApi.ts`, `GithubActivityCard.review.test.tsx` sob `clients/desktop/src/features/klm-agent`, mais relatório, plano e rollout.
- Patch entregue para a conferência específica do coordenador. Aprovação final/ativação e validação visual continuam pendentes; sem engine reiniciado, binário trocado, estado vivo alterado, commit/push, sessões/subagentes/grafos ou notificações automáticas.

## Correções entregues pela sessão implementadora — rodada 1

### R1-1 — janela expandida preservada

- `GithubActivityCard.tsx`: removido `releaseSequence` da key de Activity/children; identidade do leitor depende apenas do card e dos filtros.
- Novo `usePipelineMembers.ts`: o leitor mantém watermark, cursor, membros e limite inferior já carregado. Novas transições não substituem a janela por página 1. A mudança de composição revalida até o limite inferior no watermark antigo; `Update activity` avança explicitamente o watermark e também conserva a profundidade carregada.
- As linhas retidas mantêm suas keys e Details expandidos. A reconciliação captura/restaura a âncora do membro visível no scroller do feed; o mesmo hook atende Activity e a lista completa de filhos.
- Evidência automatizada focada: card com 21 eventos, carregamento da página 2, Details do último membro aberto, chegada de nova transição. O teste verifica manutenção do membro/Details e do watermark antigo, e só admite o novo evento após `Update activity`, ainda com o membro da página 2 presente. Teste usa React/jsdom com API simulada; não comprova geometria real do scroll.

### R1-2 — composição/TTL reconciliados sem remount

- `github_activity_groups.go`: `group.composition` é um hash dos IDs visíveis retidos e da proveniência, não da contagem/release mais recente. Não adiciona estado persistido nem lista de todos os membros ao card.
- `usePipelineMembers.ts`: composição diferente provoca reconciliação da janela antiga, inclusive sem nova release. Expiração é aplicada no render e remove os registros do estado local com o relógio ajustado do engine; o unmount das linhas descarta Details correspondentes. Se uma revalidação falha, linhas/Details em cache não ficam visíveis como conteúdo validado; Retry tenta novamente.
- Evidência automatizada focada: expiração local remove membro/Details; remoção adicional da fonte é reconciliada por composição distinta com o mesmo card e release. O teste Go também confirma alteração de composição com card/release estáveis após expiração e remoção de fontes.

### R1-3 — bookmark acompanha a origem retida

- Novo `useActivityBookmarks.ts`: seleção em memória ancorada em `detailEventId` original; a identidade atual do card é resolvida após refresh. Nenhum histórico é baixado para resolver bookmarks.
- `github_activity_api.go`/`api.go`: endpoint somente leitura `GET /api/activity/github/bookmarks`, até 100 IDs originais por request e watermark opcional. Retorna apenas evento/card/expiração para os IDs solicitados; usa a projeção elegível existente e não expõe payloads. O client envia o watermark da listagem para evitar incorporar vínculo futuro a um feed antigo. Seleções maiores são divididas em lotes.
- Provas novas remapeiam deployment/check individual para a run; filtros do cliente não apagam a seleção. Expiração/remoção da origem elimina a âncora; falha de transporte conserva intenção para a próxima atualização. Desmarcar card unido remove as âncoras que o marcaram.
- Evidência automatizada focada: testes React cobrem bookmark de deployment e check atravessando correlação tardia e posterior remoção; teste Go usa origens retidas reais em memória para provar ambos os vínculos, resolver snapshot anterior sem vínculo e omitir âncoras expiradas/removidas.
- `GITHUB_ACTIVITY_GROUPING_ROLLOUT.md` atualizado: retirada a limitação de perda do bookmark na correlação tardia; documentados composição, leitura expandida e resolver limitado.

### Verificação desta entrega

- Go, em `engine`: `go test -tags dev -run '^(TestGithubActivityGroupingIdentityAndState|TestGithubActivityGroupingPaginationAndMembers|TestGithubActivityGroupingRetentionAndBookmarks|TestGeneralFeedRead)$' .` → `ok klm/engine 1.716s`. Quatro testes focados, com compilação do pacote; sem suíte completa.
- Frontend, em `clients/desktop`: `npx vitest run src/features/klm-agent/GithubActivityCard.review.test.tsx` → um arquivo, quatro testes passaram. Cobrem janela de 21 membros/Details, atualização explícita, expiração local, remoção com release estável e bookmarks de deployment/check. São testes focados React/jsdom com APIs simuladas, não uma integração nem validação visual no engine em uso.
- `npx tsc -b --pretty false` → passou após os patches finais de watermark.
- `git diff --check` → passou; somente avisos de conversão LF/CRLF, inclusive em arquivos de trabalho alheio preservados.
- Arquivos desta rodada: `engine/api.go`, `engine/github_activity_api.go`, `engine/github_activity_groups.go`, `engine/github_activity_groups_test.go`; `GithubActivityCard.tsx`, `githubActivityApi.ts`, `KlmAgentWorkspace.tsx`, `useGithubActivityFeed.ts` e novos `usePipelineMembers.ts`, `useActivityBookmarks.ts`, `GithubActivityCard.review.test.tsx` sob `clients/desktop/src/features/klm-agent`; plano, relatório e rollout.

Conferência independente pelo coordenador, validação visual e ativação continuam pendentes; estes patches não substituem a rodada 2. Nenhum engine foi reiniciado, binário trocado ou estado vivo editado. Não houve commit/push, novas sessões, subagentes, grafos ou notificações automáticas ao coordenador. Alterações locais de harness interativo e layout de grafo foram preservadas.
