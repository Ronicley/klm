# Plano: ciclo de vida, filas e permissoes pelo agente geral

## Entrega autorizada

Este documento e um plano de implementacao, ainda nao uma implementacao concluida.
O usuario pediu que uma nova sessao KLM com OpenCode, modelo
`openai/gpt-6.1-sol-fast`, esforco `high`, implemente esta extensao agora.
A instrucao antiga de aguardar do plano da primeira slice nao se aplica a esta
entrega. Ler `AGENTS.md` e `product.md` e preservar alteracoes alheias antes de
implementar. Nao criar outras sessoes/subagentes, executar graphs, gerar
instaladores, reiniciar o engine em uso, fazer commit ou push nesta tarefa.

Autorizacao de desenvolvimento: mensagem humana
`847229ca-d8eb-4502-a6da-56dd41796ba1`, nesta conversa de planejamento.
Esse ID serve para rastrear a entrega. Nao e um evento humano da sessao receptora
nem um valor fixo a inserir nas tools implementadas.

## 1. Objetivo e escopo

Completar os controles do agente geral para que o usuario possa pedir, pelo chat:

- Criar uma sessao normal em um projeto explicitamente escolhido.
- Parar o turno de uma sessao e pausar suas mensagens ainda nao enviadas.
- Consultar a fila existente, remover uma mensagem e usar seu controle Send now.
- Arquivar e restaurar sessoes de nivel superior.
- Responder a uma permissao pendente com uma das decisoes realmente disponiveis.

Reutilizar a execucao, persistencia, fila, Stop e permissao dos chats existentes.
Manter `session_send` para instrucoes com atividade visivel e `session_ask` para
perguntas com resposta correlacionada. Nao criar outra infraestrutura de tarefas.

Fora do escopo: Data Sources, canvas, GitHub/Grafana, webhooks, agendamentos,
Telegram, presenca, monitoramento, avisos automaticos de ociosidade/conclusao,
hierarquia de reporte, geracao de instaladores e execucao de graphs pelo geral.
Tambem nao adicionar reordenacao arbitraria/edicao de mensagens, nova pasta,
transferencia de projeto ou caminhos arbitrarios de execucao.

## 2. Base concreta existente

| Area | Arquivos/operacoes existentes | Reuso |
|---|---|---|
| Catalogo global | `engine/general_agent_tools.go`: `generalAgentTools`, `callGeneralTool`, `generalTarget`, `generalSummaryLocked` | Registrar tools apenas no catalogo autenticado do geral; obter fila e pedidos pelo `session_get`. |
| Criacao por agente | `engine/session_spawn.go`, `engine/session_collaboration.go`: selecao, catalogo, `SessionSpawn`, `SpawnOrigin`, recibo duravel | Adaptar explicitamente destino por projeto para o geral, mantendo semantica das sessoes normais. |
| Criacao HTTP | `engine/api.go`: `createSession` | Referencia para sessoes normais; nao usar um POST interno para contornar validacao de spawn. |
| Stop | `engine/api.go`: `stop` | Extrair operacao compartilhada e preservar cancelamento, pausa da fila e espera pela terminacao. |
| Fila | `engine/message_queue.go`: `changeQueuedMessage`, `wakeSteeringLocked`, `scheduleMessagesLocked` | Extrair remocao/envio, mantendo os estados e as particularidades de cada harness. |
| Arquivamento | `engine/api.go`: `patchSession`, `workspace`, `folderArchived`; `conversationArchived` | Arquivo e agrupamento sao metadados; arquivar nao interrompe execucao. |
| Permissoes | `engine/permissions.go`: `permissionDecision`; `engine/permission_policy.go` | Extrair resposta compartilhada com validacao, busy, grants, rollback e resolucao relacionada. |
| Roteamento de pedidos | `engine/questions.go`: `replyConversationQuestion`; projecao de graph no `session_get` | Usar o chat dono + ID do pedido para resolver pedidos de nodes, sem conceder controle direto dos nodes. |
| Bridge e clientes | `engine/linked_bridge.go`, Pi extension, clientes MCP OpenCode/Codex | Preservar catalogo por capacidade e orcamentos de timeout. |
| Persistencia | `engine/store.go`, `engine/graph_records.go`, `engine/stream_journal.go` | Validar a nova origem de spawn tambem no carregamento e replay. |

Extrair helpers pequenos dos handlers quando necessario. API HTTP e tools devem
compartilhar a operacao interna; nao chamar a propria API via rede, montar
ResponseWriter falso ou duplicar validacao/mutacao em um segundo servico.

## 3. Catalogo proposto

Os nomes abaixo compoem o catalogo privado do geral; nao ampliar o catalogo global
dos agentes normais. As opcoes de criacao sao distintas do spawn implicitamente
vinculado ao projeto de uma sessao comum.

| Tool | Entrada principal | Resultado |
|---|---|---|
| `session_create_options` | `projectId`, `harness?`, `cursor?`, `limit?` | Harnesses instalados, pastas ativas, modelos/esforcos/defaults no destino e IDs de mensagens humanas recentes do geral. |
| `session_create` | `projectId`, `title`, `prompt`, `operationId`, `sourceUserEventId`, `harness?`, `model?`, `effort?`, `folder?`, `yolo?` | Recibo de aceite persistido com IDs da sessao e mensagem, configuracao e projeto. |
| `session_stop` | `sessionId` | Estado apos Stop ou erro preciso de cancelamento ainda em andamento/falha de terminacao. |
| `session_queue_remove` | `sessionId`, `messageId` | Estado da fila apos remocao do item exato. |
| `session_queue_send` | `sessionId`, `messageId`, `retryUncertain?` | Aceite do Send now e ID efetivo do item; nao afirma execucao/conclusao. |
| `session_archive` | `sessionId` | Arquivamento proprio e efetivo, com estado de execucao preservado. |
| `session_restore` | `sessionId`, `folder?` | Restauracao; pasta opcional permite retirar a sessao de uma pasta arquivada de forma atomica. |
| `session_permission_reply` | `sessionId`, `permissionId`, `decision`, `sourceUserEventId?` | Resultado da resposta exata e estado atualizado do chat dono. |

`session_get` ja lista a fila e todos os pedidos. Nao criar uma segunda tool de
listagem de fila sem necessidade. Resultados novos usam os limites e fragmentacao
existentes e nao retornam historico completo. Todos os controles usam IDs estaveis.

## 4. Identidade e limites de acesso

- A capacidade global vem da bridge autenticada ligada ao adapter e ao turno do
  singleton geral. Argumentos `projectId`/`sessionId` nao concedem essa capacidade.
- As tools operam apenas em projetos cadastrados e nao removidos deste engine.
- `session_create` cria apenas chats normais de nivel superior. Nunca cria outro
  geral, side chat, graph node ou subagente nativo.
- Stop/fila/permissao podem enderecar os chats main e side ja admitidos por
  `generalTarget`. Arquivar/restaurar admite somente chats normais superiores.
- O geral nunca para, arquiva ou manipula sua propria fila/permissoes por estas
  tools. Seus controles de interface permanecem normais.
- Nao aceitar graph nodes/subagentes nativos como destinos diretos. Uma permissao
  projetada no chat dono pode ser respondida por esse dono e pelo pedido exato.
- Leitura de arquivados continua disponivel. Remocao de fila, Stop e resposta a
  pedidos existentes sao controles de recuperacao e podem funcionar em chats
  arquivados; novo envio pela fila exige restauracao efetiva primeiro.
- Revalidar origem ativa, disponibilidade e identidade do destino sob lock antes
  da mutacao, especialmente apos catalogos ou outras esperas fora do lock.
- Nenhuma dessas ferramentas executa graphs nem converte instrucoes/recibos/
  respostas de agente em eventos humanos de autorizacao.

## 5. Criacao: reusar spawn sem projeto ficticio

Criacao inclui titulo e prompt inicial autocontido, como o spawn existente.
`projectId` e obrigatorio porque o geral nao possui projeto. O diretorio efetivo e
sempre o diretorio registrado desse projeto, nao `workspaces/general-agent`.
`folder` e apenas agrupamento visual: omissao significa `Ungrouped`; nao herdar
um agrupamento do geral e nao aceitar pasta arquivada ou inexistente.

Preservar as regras de settings do spawn: harness omitido herda o geral; modelo e
esforco podem herdar quando compativeis com o mesmo harness/modelo; em outro
harness usar defaults e validar o par. YOLO omitido herda o valor atual do geral;
`yolo:false` e `yolo:true` sao overrides explicitos aplicados antes do primeiro
turno. Descrever isso nas opcoes e no prompt; nao mudar YOLO automaticamente para
facilitar a tarefa. Mudancas posteriores no geral nao alteram a sessao criada.

Consultar catalogo no diretorio do projeto de destino fora de `app.mu`, usando os
modelos reais conectados. OpenCode usa `provider/model`. Revalidar configuracao,
projeto, diretorio, pasta e turno antes de um unico commit de sessao + mensagem
na fila + payload + recibo. Rejeicoes de validacao nao criam sessao nem reservam
`operationId`. Nao manter ponteiros de Session atraves de operacoes que podem
realocar o slice de sessoes.

`sourceUserEventId` deve identificar uma mensagem real de tipo `user` na conversa
geral que pediu a criacao; nao aceitar `agent_prompt`, consultas ou um evento de
outro chat. E rastreabilidade, nao prova semantica de permissao. A instrucao
entregue ao filho continua sendo `agent_prompt` com origem e nao autoriza novos
spawns/graphs como se fosse usuario humano.

Reutilizar `SessionSpawn` e sua chave por remetente/operationId. Incluir projeto
explicitamente no request normalizado do geral para que repetir a chave com
outro destino resulte em conflito. Pode ser um campo opcional adicional no
request existente, com significado restrito ao remetente geral; preservar leitura
dos recibos antigos e igualdade das solicitacoes normais. Recibo identico retorna
o mesmo filho mesmo depois de consumo da fila ou reinicio.

Hoje `existingSpawnLocked`, `spawnSelectionLocked`, `spawnOptions` e o validador
de `SessionSpawns` em `store.go` assumem remetente normal e mesmo projeto. Abrir
uma excecao explicita para origem geral e destino top-level normal, incluindo
validadores de checkpoint/replay pertinentes. Nao relaxar `consultationEndpoint`
globalmente nem permitir que sessao comum forneca outro projeto. Opcao/cursor
de catalogo do geral deve se vincular tambem ao projeto/harness escolhidos.

Recibo significa criacao e aceite, nao que o trabalho iniciou/terminou. Nao
adicionar assinatura, espera automatica ou notificacao de conclusao ao criador.

## 6. Stop e fila

Extrair Stop preservando sua ordem atual: incrementar `stopVersions`, cancelar o
turno antes de depender de uma gravacao bem-sucedida, pausar `queued`/`steering`,
cancelar consultas relacionadas e aguardar `t.done` fora do lock por ate 20s.
Preservar `stopErr`, o descarte do runtime e a pausa apos reinicio/erro. Se o
pedido expirar apos iniciar cancelamento, nao alegar que nada aconteceu; informar
estado incerto/em andamento e orientar uma leitura do estado antes de repetir.

Stop interrompe o chat e seus processos nativos possuidos. NAO interrompe um
graph independente invocado pelo chat. Manter `activeGraph` no resultado para
deixar essa diferenca observavel; nao prometer um Stop de graph inexistente.

O servidor da bridge tem deadline padrao de escrita de 12s. Incluir
`session_stop` no tratamento de orcamento longo para acomodar seus 20s. Criacao
e opcoes tambem precisam do orcamento de catalogo ja adotado: catalogo 45s,
bridge 55s e clientes 60s. Nao modificar a espera especial de `ask`.

Remover/envio reutiliza os controles de `changeQueuedMessage`:

- Item `sending` nao pode ser removido/enviado novamente; turno ainda parando e
  engine encerrando continuam gerando conflitos.
- Remover tira somente o item da fila e seu payload; nao remove historico ja
  aceito nem cancela uma execucao iniciada.
- Send now em chat ativo usa steering no limite suportado pelo harness; quando
  idle promove o item para a frente e inicia um turno. Nao muda modelo/sandbox,
  nao para o turno e nao representa reordenacao arbitraria.
- Preservar conteudo preparado, referencias, origem de agente/usuario e IDs;
  enviar item humano pela tool nao o reescreve como uma nova mensagem humana.
- `uncertain` indica que pode ter havido entrega nativa. Send now existente cria
  outro ID para uma tentativa explicita. Na tool exigir `retryUncertain:true`
  para esse caso e descrever que pode duplicar uma instrucao ja recebida. O prompt
  exige decisao explicita do usuario para essa recuperacao; nao tentar sozinho.
  Retornar o ID substituto. Preservar o caminho de recuperacao manual da UI.
- Repeticao de um ID ja consumido/removido deve retornar ausencia/conflito, sem
  recriar item. Nao adicionar outra tabela de recibos para a fila nesta slice.

## 7. Arquivar e restaurar

Compartilhar validacao/mutacao com `patchSession`. Arquivamento e organizacao:
nao cancelar turno, pedidos pendentes, side chat ou graph, nao apagar historico
e nao pausar/reiniciar a fila implicitamente. A UI atual permite arquivar uma
sessao trabalhando; manter essa semantica, com retorno do estado real.

Arquivamento efetivo depende tanto de `Session.Archived` quanto da pasta. Em
`session_restore`, se a pasta atual estiver arquivada e nenhuma pasta ativa tiver
sido fornecida, retornar conflito explicando que e preciso escolher uma pasta
ativa ou `Ungrouped`. Nao desarquivar uma pasta inteira com outras sessoes. Com
`folder`, validar destino no mesmo projeto e aplicar movimento + Archived=false
num commit unico. Nunca mover arquivos ou alterar executionCwd. Repetir arquivo
ou restauracao ja satisfeitos pode ser no-op com estado atual.

## 8. Responder permissoes

Extrair `permissionDecision` para uma operacao compartilhada que preserve:
pedido realmente pendente no turno atual, validacao contra `request.Decisions`,
flag `busy`, gravacao de `Resolving`, callback nativo fora do lock, rollback do
grant em caso de falha, remocao exata do pedido e resolucao de pedidos relacionados.
Nao criar grants antes de validar a decisao e nao substituir a resposta por YOLO.

Decisoes existentes: `once`, `session`, `always` (projeto), `reject`,
`deny_project`, `allow_global`, `deny_global`. Expor somente o que o pedido aceita.
O prompt deve preservar a abrangencia pedida pelo usuario: nao ampliar uma
aprovacao pontual para projeto/global. Retorno e historico devem identificar
decisao, escopo e origem do agente geral; a resposta nao e evento humano.
Referencia humana opcional, se fornecida, deve ser validada na conversa geral.

Para graph nodes, resolver `permissionId` somente entre os pedidos atualmente
projetados no chat dono, analogamente a `replyConversationQuestion`. Depois
validar o turno nativo exato. Grants de sessao pertencem ao node nativo, nao ao
chat geral; grants de projeto pertencem ao projeto do node. Pedidos de subagentes
nativos continuam passando pelo adapter dono. IDs expirados/duplicados/busy
geram conflito, sem aprovar outro pedido semelhante. Preservar rollback e
semantica de falha; timeout apos resposta nativa nao e prova de nao entrega.

## 9. Sequencia de implementacao

1. Confirmar diff atual e extrair helpers de Stop, fila, arquivo/restauracao e
   permissoes, mantendo os contratos HTTP. Sem refatoracao ampla.
2. Adaptar criacao/opcoes e validacao persistida para o destino explicito do
   geral; preservar todos os caminhos de spawn normal.
3. Adicionar catalogo, dispatch, timeout e resultados limitados das tools.
   Adicionar origem aos eventos de controle onde necessario com os mecanismos
   existentes, sem uma infraestrutura nova de auditoria.
4. Atualizar `engine/prompts/general-agent.md`, `product.md` e `AGENTS.md` para
   remover somente as exclusoes destas capacidades. Continuam proibidas tools
   implicitamente vinculadas a projeto/side/graph e escopo global em sessoes
   normais. Documentar criacao explicita, Stop de chat, recuperacao de fila e
   abrangencia de permissoes. Atualizar docs tecnicos diretamente pertinentes.
5. Fazer uma verificacao minima de compilacao. Se houver alteracao de script
   embarcado, checar sua sintaxe; se houver TypeScript alterado, usar typecheck.
   Nao executar full suites, criar testes complexos/integracao ou chamar agentes
   de revisao. Testes existentes focados podem ser usados apenas se necessarios
   para esclarecer uma falha concreta. Revisar diff e relatar limites restantes.

## 10. Aceitacao manual e criterios de entrega

Nao alterar sessoes reais do usuario para demonstrar tools nesta implementacao.
Apresentar estes cenarios para validacao humana em conversas descartaveis:

- Geral cria no projeto escolhido com titulo/prompt/configuracao; repetir o
  operationId nao duplica, mudando projeto na mesma chave conflita; referencia
  humana falsa/agent_prompt, pasta invalida e modelo indisponivel nao criam nada.
- Um novo filho inicia no diretorio do projeto com YOLO herdado ou override
  solicitado, e seu trabalho aparece na timeline normal. Reinicio preserva
  sessao, origem, fila e recibo, incluindo dados anteriores a esta extensao.
- Stop de chat trabalhando/aguardando pergunta ou permissao cancela o chat e
  pausa entradas, mantendo graph independente ativo quando houver. Encerramento
  lento nao e confundido com falha imediata por timeout de 12s.
- Fila pode ser lida, item exato removido e item enviado agora com o comportamento
  atual de steering/idle. Sending conflita e uncertain exige recuperacao explicita.
- Arquivar durante execucao preserva trabalho/pedidos; restaurar sob pasta
  arquivada exige destino ativo, sem mudar as outras sessoes da pasta.
- Permissao pontual/session/projeto/global respeita as decisoes disponiveis;
  pedido expirado/duplicado nao aprova nada; falha nativa mantem recuperacao e
  rollback. Pedido projetado usa dono correto, sem expor controle direto de node.
- Pi, OpenCode e Codex recebem o mesmo catalogo apropriado ao geral. Sessoes
  normais continuam com o escopo anterior e nao ganham estas tools globais.

Entrega: alteracoes focadas + resumo de arquivos, verificacao leve efetivamente
executada e cenarios ainda dependentes de validacao humana. Sem commit/push.
