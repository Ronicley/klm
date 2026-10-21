# Plano — cards atualizáveis de workflows e deployments

Status: planejamento e delegação autorizados em 2026-10-15; implementação, duas rodadas de review/correção e aplicação ao feed existente pendentes.

## 1. Objetivo e escopo aprovado

Reduzir a repetição do feed agrupando somente ciclos de workflow runs e deployments. Um card mostra status atual, jobs/etapas reais e histórico expansível; sem percentual estimado. PRs, issues, pushes, criação de branches e demais eventos continuam individuais.

- Workflow: um card por run/tentativa; jobs distintos são linhas distintas, inclusive jobs paralelos ou skipped.
- Deployment: um card por deployment com ambiente, estado e histórico. Incorporar ao card da run somente com vínculo explícito e inequívoco; associar a um job específico somente quando esse vínculo também estiver comprovado.
- Checks/suites podem complementar a execução ou job mediante vínculo confirmado. Sem vínculo seguro, preservar posts individuais; não implementar um agrupador genérico de checks/PRs.
- Preservar success, failure, cancelled, skipped, waiting e demais estados reais. Resultado do workflow não substitui resultado de job ou deployment.
- Eventos originais permanecem imutáveis e consultáveis enquanto retidos. Agrupamento visual não é deduplicação de entregas.

## 2. Base confirmada e limitações

Ler AGENTS.md e product.md antes da implementação. O feed atual é uma projeção de releases, sem coleção durável independente de posts.

- `engine/webhooks.go`: WebhookStoredEvent guarda DeliveryID e Payload original, imutável até remoção/expiração.
- `engine/github_activity.go`: normalização compacta, cache de resumos e Details com allowlist/truncamento. Os posts não contêm todos os IDs/relações/timestamps necessários.
- `engine/github_activity_api.go`: projeção global, filtros, paginação descendente por release e leitura compartilhada com ferramentas. Conferir as alterações locais antes de editar.
- `engine/general_agent_tools.go`: leitura do feed pelo agente geral; preservar compatibilidade e explicar os novos cards/histórico no contrato.
- `clients/desktop/src/features/klm-agent/githubActivityApi.ts`, `useGithubActivityFeed.ts` e componentes/estilos do feed: DTOs, refresh, paginação, bookmarks e preservação de posição.
- O plano anterior `GITHUB_ACTIVITY_FEED_IMPLEMENTATION_PLAN.md` documenta as invariantes; este documento altera somente o agrupamento aprovado.

O snapshot fornecido pelo usuário não inclui payloads completos. Não afirmar que todos os vínculos existem. Inspecionar payloads retidos por acesso local/API somente de leitura, se disponíveis, sem publicar conteúdo sensível. Não abrir outras sessões para obtê-los. Se indisponíveis, documentar a limitação e usar fallback conservador.

Modificações já presentes ao delegar, a preservar: `engine/README.md`, `engine/general_agent_tools.go`, `engine/general_agent_tools_test.go`, `engine/github_activity_api.go`, `engine/prompts/general-agent.md`, `product.md`. Inspecionar diff atual; não restaurar arquivos de HEAD nem sobrescrever trabalho alheio.

## 3. Implementação proposta

### A. Extrair metadados e comprovar correlações

Extrair no backend metadados tipados a partir do payload, sem enviar payload bruto em cada linha do feed. Preservar acesso ao original pelo mecanismo já existente; não ampliar desnecessariamente ferramentas privadas.

- Escopo de identidade: projeto, conexão/webhook, provider e repository.id.
- Run: ID real, run_number para exibição, workflow_id, run_attempt, SHA completo, branch, status/conclusion e timestamps originais disponíveis.
- Jobs/checks/suites: IDs próprios e relações explícitas à run/tentativa/job/suite. Igualdade de nomes, SHA, branch, horário ou números apresentados não comprova equivalência entre tipos.
- Deployment/status: IDs próprios, ambiente/alvo, SHA/ref, timestamps e relação explícita à execução/tentativa quando existir.
- Guardar proveniência dos membros do grupo e das relações utilizadas. IDs grandes devem preservar precisão no JSON/TypeScript.
- Dado ausente permanece ausente. Não assumir attempt=1 nem associar automaticamente à tentativa mais recente. Relações ambíguas mantêm o evento individual.

### B. Projetar cards e histórico no backend

Preferir projeção derivada das fontes existentes; não criar armazenamento paralelo de payloads nem modificar ingresso, dedupe por delivery, releases ou TTL.

- Chave de workflow inclui run ID + tentativa; chave de deployment inclui deployment ID, ambas com o escopo de origem.
- Atualizar estado pelo significado e timestamps da fonte; releaseSequence representa recebimento/liberação, não cronologia do GitHub. Evento atrasado não pode regredir indevidamente um resultado concluído.
- Não aplicar uma ordem linear universal a todos os estados: deployment pode receber novas atualizações legítimas, como inactive, após success.
- Nova tentativa/run/deployment tem identidade própria; preservar falha anterior e resultados divergentes dos filhos. Job novo é outro subitem.
- Histórico expansível mantém eventos membros, ações, horários, resultados e acesso aos detalhes. Limitar/paginar histórico grande em vez de truncar silenciosamente.
- Agrupar sobre o conjunto elegível antes de paginar cards, não apenas dentro da página carregada. Membros espalhados por várias páginas não podem produzir cards duplicados/incompletos por esse motivo.
- Definir IDs estáveis de cards, ordem determinística, watermark/cursor versionados se necessário e refresh sem perder/duplicar cards. O snapshot do cursor não deve incorporar membros posteriores ao seu watermark.
- Documentar filtros e busca: respeitar famílias desabilitadas, sem revelar seu conteúdo através de grupos. Correlacionar identidades antes de montar a apresentação filtrada; não apresentar como resultado completo o que foi omitido por filtro. Busca encontra card pelo conteúdo dos membros visíveis.
- Preservar leitura de post original por ID, limites de bytes das ferramentas e acesso ao histórico. Se a listagem do agente passar a retornar grupos, tornar explícita a distinção entre cards e eventos e oferecer acesso aos membros retidos.
- Expiração/remoção de fontes deve remover também seus dados derivados; sem prolongar TTL nem ressuscitar conteúdo. Não inventar começo/fim para histórico parcial.

### C. UI

- Reusar componentes/tokens e hierarquia visual atual. Texto controlado pela aplicação em inglês.
- Cabeçalho: workflow/run/tentativa ou deployment/ambiente, referência e status geral.
- Jobs: lista compacta com identidade e resultado individual; estados paralelos e skipped explícitos. Usar indicadores indeterminados apenas enquanto houver atividade, respeitando reduced motion.
- Deployments associados: linhas próprias, sem confundir sucesso da run com sucesso da implantação. Deployment isolado mantém seu próprio card.
- `Activity` expansível com histórico; preservar `Details`, links seguros e bookmarks.
- Atualizar o conteúdo sem deslocar a leitura a cada transição. Compatibilizar refresh, aviso de novidades, carga de páginas antigas, filtros e bookmarks com a identidade agrupada.
- Mesma apresentação no desktop, navegador/Focus e WebView responsiva.

## 4. Exemplo concreto e critérios de aceite

Snapshot de referência: 80 posts retidos, seqs 11–90, asOf 2026-10-14T23:36:14Z. Todos do projeto Cubiq, repo LegistCore/frontend. Não é garantia da população disponível no momento da migração.

- Run #264: seqs 69–74, 76–79, 82, 84–87, 89–90; jobs 116111677121 (Verify) e 116118161233 (Build and deploy VPS), suite 104708539551.
- Deployment 7077863283: seqs 75, 80–81, 83, 88. Incorporar à run #264 somente se comprovado.
- Run #263: seqs 55–66, jobs 116098432887 e 116105228759, suite 104697863582.
- Run #262: seqs 37–41 e 45–51, jobs 116044781262 e 116051774829, suite 104654101128.
- Run #261: seqs 20–24 e 27–33, jobs 116008676644 e 116015697869, suite 104624268547.
- Runs #261–263 têm Verify success e Build and deploy VPS skipped: ambos precisam permanecer explícitos.
- Os 22 posts restantes (PRs, push, create e issues) permanecem individuais. Com todas as relações propostas confirmadas, 58 notificações de pipeline virariam 5 cards, ou 4 com deployment incorporado: total de 27 ou 26 cards. Isso é estimativa condicional, não meta a atingir por heurística.

Aceite: identidade comprovada; nenhuma fusão entre tentativas; nenhum sumiço de falhas/skipped; histórico acessível; paginação sem duplicação; filtros/TTL preservados; eventos antigos e novos seguem as mesmas regras.

## 5. Validação e duas rodadas de review/correção

O usuário autorizou duas rodadas de review e correção entre o agente coordenador e a sessão implementadora. Isso não autoriza suíte completa nem novas sessões/subagentes.

1. Implementador entrega código, arquivos alterados, evidências dos vínculos, decisões de paginação/filtros, checks leves e limitações, além do procedimento para aplicar ao feed atual. Não executar a migração final ainda.
2. Review 1 pelo coordenador: identidade/correlação, tentativas, estados fora de ordem, proveniência, retenção e compatibilidade de API/paginação. Enviar achados concretos via session_send; implementador corrige e registra evidências.
3. Review 2 pelo coordenador: verificar correções e comportamento UI/refresh/histórico/filtros, mais preparo da migração. Enviar achados remanescentes; implementador corrige. Conferir as correções antes de liberar a aplicação final.
4. Checks mínimos apropriados: compilação Go e TypeScript para os arquivos afetados; verificações focadas adicionais apenas quando necessárias para achados concretos. Não rodar suítes completas nem criar testes de integração complexos sem novo pedido.
5. Registrar por rodada achados, resolução e verificações realmente executadas. Não declarar review ou validação humana concluídos por intenção.

Sessões são independentes: criação não espera nem coleta conclusão automaticamente. Sem polling/monitoramento. O usuário sinaliza o retorno à sessão implementadora para iniciar cada etapa posterior; manter as etapas pendentes até haver resultado real.

## 6. Aplicação/migração do feed existente — etapa final

Objetivo: posts já retidos devem aparecer agrupados, não apenas novas entregas. A implementação deve preparar isso desde o início; executar a aplicação ao feed em uso somente depois das duas rodadas e correções.

- Preferir reprocessar a projeção a partir dos payloads/releases existentes. Se não houver novo estado persistido, a migração pode ser simplesmente ativação da versão + invalidação do cache + refresh de cursores, sem reescrever eventos.
- Se precisar de metadados persistidos, usar backfill versionado, idempotente e retomável, preservando payloads, IDs de evento, delivery IDs, releaseSequence, recebimento, expiração e pendências. Não reenviar webhooks nem criar releases artificiais.
- Documentar o procedimento exato, engine alvo (dev ou instalado), versão e rollback. Não editar o estado vivo no disco nem trocar binários/reiniciar engine durante a implementação/reviews. Confirmar o alvo com o usuário na aplicação final se não estiver inequivocamente identificado.
- Antes de eventual escrita persistente, obter cópia consistente/backup adequado; aplicar pelo mecanismo suportado do engine. Eventos expirados ou removidos continuam indisponíveis.
- Comparar a população atual antes/depois: todos os eventos retidos elegíveis devem estar representados como card individual ou membro de grupo. Reportar cards, membros agrupados, fallbacks e relações não comprovadas. Não exigir os números do snapshot antigo se a retenção/população mudou.
- Validar visualmente a aplicação com o usuário e documentar o resultado real. Não declarar migração concluída apenas por entregar o código.

## 7. Estado de execução

- [x] Plano escrito.
- [x] Sessão implementadora criada: `Feed — workflow runs e deployments agrupados`, ID `adcb15b229f7abb87af81c3eb589bba4` (OpenCode, `openai/gpt-6.1-sol`, `high`; YOLO herdado habilitado). Criação aceita; não comprova início ou conclusão.
- [x] Implementação entregue e checks leves registrados (2026-10-15): projeção v2 derivada dos releases retidos, IDs estáveis por run/tentativa ou deployment, correlação explícita de jobs/checks/deployments, histórico/filhos paginados, UI de jobs/Activity e contrato `feed_read` ampliado preservando Details e limites de bytes.
- [x] Review/correção 1 concluídos.
- Review 1 realizado pelo coordenador; patches dos três achados entregues em 2026-10-16, com evidências em `GITHUB_ACTIVITY_GROUPING_REVIEW.md`: janela expandida preservada por snapshot, composição/TTL reconciliados e bookmarks ancorados em eventos originais. Patches conferidos pelo coordenador na rodada 2, com residual no TTL do feed pai (R2-1 abaixo). Entrega efetiva localizada na sessão ativa `Implementação feed group`, ID `b7b00428fd28eef46e58bd0b70025f89`; sessão original arquivada após falhas de inicialização.
- [x] Review/correção 2 concluídos e correções conferidas.
- Review 2 realizado em 2026-10-16: patches anteriores conferidos, quatro testes React passaram e comando Go focado retornou sucesso pelo cache. Regressão residual R2-1 reproduzida pelo coordenador com teste do hook real; patch corretivo entregue, aguardando sua conferência específica. Detalhes em `GITHUB_ACTIVITY_GROUPING_REVIEW.md`.
- [x] Feed existente atualizado; integridade/API/UI verificados pelo coordenador após o retorno do usuário. Aceite visual pessoal do usuário permanece manual.

### Entrega da implementação — fatos concluídos

- Alterações locais anteriores foram inspecionadas e preservadas; `engine/general_agent_tools_test.go` continua apenas com o trabalho que já existia ao receber a tarefa. Ingresso, payloads, dedupe, persistência e TTL não foram alterados. Não houve commit/push, novas sessões, subagentes ou grafos.
- Inspeção somente leitura via API instalada `127.0.0.1:7331`: 80 eventos, watermark `90`; 58 de pipelines. Vinte eventos de jobs contêm run/tentativa; os 16 eventos de checks têm URL exatamente correspondente à `check_run_url` de jobs retidos. Deployment `7077863283` está explicitamente ligado ao job `116118161233` da run `38639669364`, tentativa 1. Quatro suites não possuem tentativa e ficam individuais. Evidências/procedimento detalhados em `GITHUB_ACTIVITY_GROUPING_ROLLOUT.md`.
- A expectativa para essa população, segundo as regras implementadas, é 30 cards: quatro runs representando 54 eventos, quatro suites individuais e 22 outros eventos individuais. Essa contagem não foi medida em uma projeção v2 ativada; não representa migração concluída.
- Go: `go test -tags dev -run '^(TestGithubActivityGroupingIdentityAndState|TestGithubActivityGroupingPaginationAndMembers|TestGeneralFeedRead)$' .` passou; somente dois testes focados de agrupamento e o teste existente de compatibilidade do feed geral. Compilação do pacote incluída nessa execução.
- TypeScript: `npx tsc -b --pretty false` passou, inclusive após os ajustes finais de UI. `git diff --check` passou (apenas avisos de conversão LF/CRLF do Git).
- Aplicação ao feed existente preparada como reconstrução idempotente da projeção: ativar engine/client aprovados, cache novo e refresh de cursores; sem backfill persistido ou reescrita de eventos. Documento de rollout diferencia dev/instalado, comparação de membros e rollback.
- Reviews 1 e 2, ativação, reinício/troca de binários e validação humana permanecem pendentes. Não foram realizados suíte completa, smoke de integração ou validação visual.

### Correções da rodada 1 — 2026-10-16

- R1-1: leitor expandido conserva watermark e janela carregada; novidades são admitidas por `Update activity`, sem remount por release. R1-2: revisão de composição dos membros dispara reconciliação no snapshot aberto; TTL local remove linhas/Details. R1-3: bookmarks em memória usam origem individual e resolução limitada evento → card, no watermark exibido, preservando intenção na correlação tardia.
- Go: quatro testes focados passaram (`TestGithubActivityGroupingIdentityAndState`, `TestGithubActivityGroupingPaginationAndMembers`, `TestGithubActivityGroupingRetentionAndBookmarks`, `TestGeneralFeedRead`), resultado `ok klm/engine 1.716s`.
- Frontend: quatro testes focados em `GithubActivityCard.review.test.tsx` passaram; `npx tsc -b --pretty false` e `git diff --check` passaram após os ajustes finais. Evidências/arquivos por achado constam de `GITHUB_ACTIVITY_GROUPING_REVIEW.md`.
- Patches entregues; conferência do coordenador, rodada 2, geometria visual/scroll e ativação do feed vivo continuam pendentes. Trabalho alheio preservado; nenhum reinício, binário, migração ou estado vivo alterado.

### Correção residual da rodada 2 — 2026-10-16

- R2-1: menor expiração dos membros passa a disparar revalidação imediata do grupo sem removê-lo preventivamente. Um shell transitório sem snippets vencidos preserva os leitores montados; a API confirma sobreviventes ou remoção final. Falha usa o erro/Retry existente, sem restaurar dados vencidos. Posts individuais mantêm remoção no TTL.
- Teste original do coordenador preservado e aprovado no check local; adicionados somente cenários focados de páginas/Details sobreviventes, remoção final, falha e recuperação. `npx vitest run src/features/klm-agent/GithubActivityCard.review.test.tsx` passou com sete testes; `npx tsc -b --pretty false` passou. Nenhum Go alterado ou suíte completa executada nesta correção.
- Arquivos e evidências no relatório da rodada 2 e no rollout. Conferência específica/aprovação final do coordenador, validação visual e ativação permanecem pendentes. Nenhum engine, binário ou estado vivo alterado.

### Fechamento das duas rodadas pelo coordenador — 2026-10-16

- Correção R2-1 conferida no hook do feed e na renderização: a expiração parcial limpa o conteúdo vencido, preserva o card/leitor e solicita revalidação; ausência de sobreviventes remove o card após resposta. Falha mantém recuperação via Retry sem restaurar snippets vencidos.
- Coordenador executou `npx vitest run src/features/klm-agent/GithubActivityCard.review.test.tsx`: **7 testes passaram**, incluindo a regressão originalmente reprovada, identidade montada, página 2/Details sobreviventes, remoção final e falha/recuperação. O teste original não teve suas asserções enfraquecidas.
- Revisões e correções encerradas no escopo combinado; os registros históricos de pendência acima foram superados por esta conferência. Não houve nova rodada ampla, suíte completa ou validação visual de geometria/scroll.
- Próxima etapa: definir com o usuário o alvo de ativação (instalado ou dev), aplicar a nova projeção aos eventos retidos e comparar a população antes/depois. **Feed em uso ainda não atualizado nesta tarefa.**

### Validação dev e preparação do instalado — 2026-10-16

- Usuário autorizou dev primeiro e instalado se dev passasse. Dev validado com 106 eventos reais reenviados apenas ao ambiente isolado: 39 cards/6 grupos, união integral sem duplicações, API paginada/Details, UI e reconstrução após restart aprovados nas verificações focadas.
- Builds instaláveis locais preparados; aplicação instalada requer manutenção independente do engine que hospeda esta conversa. Procedimento, hashes e localização do resultado em `GITHUB_ACTIVITY_GROUPING_ACTIVATION.md`. Ativação não declarada concluída até leitura do resultado pós-reinício.

### Ativação instalada concluída — 2026-10-16

- Resultado pós-reinício lido e confirmado: manutenção `success`, tarefa Windows com resultado 0; 106 eventos preservados em 39 cards/6 grupos, watermark 116. Hashes de payload, metadados e binários conferidos pelo processo de manutenção.
- Coordenador confirmou contagens diretamente na API e verificou frontend instalado com Playwright: jobs/deployment, histórico paginado de 21 membros, Details, skipped e failure. Zero erros/avisos no console.
- Sem reescrita de eventos, replay no instalado, commit ou push. Backup dos binários/prompts anteriores disponível. Evidências finais em `GITHUB_ACTIVITY_GROUPING_ACTIVATION.md` e no `activation-result.json` temporário.
