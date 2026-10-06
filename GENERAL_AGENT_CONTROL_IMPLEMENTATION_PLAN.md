# Plano: ferramentas de consulta e controle do agente geral

## Estado e instrução de entrega

Este documento é um plano de implementação, não uma implementação concluída.
Foi solicitado pelo usuário em 2026-10-06 para entrega a uma nova sessão KLM com
OpenCode, modelo `openai/gpt-6.1-sol-fast`, esforço `high`.

**A sessão que receber este plano deve apenas acusar recebimento e aguardar.
Não começar a implementação, editar arquivos, executar validações, criar outras
sessões ou fazer commit/push até o usuário pedir explicitamente para começar.**

Quando o início for autorizado, ler `AGENTS.md` e `product.md`, verificar o estado
atual do repositório e preservar alterações alheias. A solicitação atual aprova o
planejamento destas capacidades; não autoriza executar graphs nem modificar
sessões reais para demonstrá-las. Não gerar instaladores ou reiniciar a engine em
uso como parte da implementação.

## 1. Objetivo e limites

Permitir que o usuário consulte e opere sessões dos projetos cadastrados no mesmo
engine através de sua conversa com o agente geral. Reutilizar as sessões, adapters,
histórico, fila, perguntas e configurações já existentes. O engine continua sendo
a camada de interface e persistência; os harnesses executam o trabalho.

Capacidades solicitadas:

- Descobrir projetos, pastas de organização e sessões ativas/inativas/arquivadas.
- Identificar nome, projeto, pasta visual, diretório efetivo, estado, harness,
  modelo, esforço, YOLO, graph selecionado e uso da janela de contexto.
- Listar e buscar mensagens; ler eventos e atualizações parciais da execução.
- Enviar ordens para execução visível e fazer consultas com resposta correlacionada.
- Renomear sessões, movê-las de pasta visual, alterar modelo/esforço/YOLO e selecionar
  um graph disponível no projeto da sessão.
- Consultar perguntas pendentes e responder à question tool na sessão de origem.

A nova tool de ordens também deve estar disponível para agentes de sessões normais,
com seu escopo atual de colaboração. A visão global é exclusiva do agente geral.

Fora desta entrega:

- Notificações automáticas de ociosidade, encerramento, perguntas ou permissões;
  caixa de notificações, assinaturas, monitoramento automático e loops de polling.
- Hierarquias novas de responsabilidade/reporte e modelo adicional de sumarização.
- Retomada da feature Tasks ou sua estrutura de banco de dados.
- Execução de graphs pelo agente geral, alteração das regras de autorização de
  graphs, edição dos graphs ou manipulação de seus nós como sessões comuns.
- Mudança do diretório de execução ao mover uma sessão de pasta; transferência de
  sessão para outro projeto; seleção de outro harness após o bloqueio existente.
- Nova UI de painel global, novo runtime, Hermes ou MCP público.

A resposta correlacionada de uma pergunta expressamente feita com `ask` continua
existindo. Ela não é uma notificação automática de término de trabalho.

## 2. Base existente e pontos de extensão

| Área | Implementação existente | Uso no plano |
|---|---|---|
| Agente geral | `engine/general_agent.go`, `engine/prompts/general-agent.md` | Identidade singleton sem ProjectID, workspace próprio e prompt dedicado. |
| Inventário e histórico | `engine/history.go`, `engine/api.go`, `engine/usage.go` | Projeções resumidas, páginas de eventos, mudanças por revisão, contexto e controles. |
| Bridge privada | `engine/linked_bridge.go`, `engine/graph_adapter.go` | Catálogo e dispatch com capacidade vinculada ao adapter/turno. |
| Três harnesses | `engine/pi_interactive.go`, `engine/pi-permissions.ts`, `engine/codex_interactive.go`, `engine/runtime.go` | Distribuição do mesmo catálogo interno; OpenCode/Codex por MCP e Pi pela extensão. |
| Consultas | `engine/linked.go`, `engine/linked_bridge.go` | `linked_ask`/`linked_answer`, espera limitada, fila, resultado/continuação correlacionada. |
| Origem e fila | `engine/session_collaboration.go`, `engine/message_queue.go`, `engine/api.go` | `SpawnOrigin`, evento `agent_prompt`, aceitação persistida e execução normal. |
| Configurações | `engine/models.go`, `engine/permission_policy.go`, `engine/api.go` | Modelos/esforços, YOLO, nome/pasta e graph selecionado. |
| Perguntas | `engine/questions.go` | Validação e resposta ao pedido nativo ainda ativo. |
| Persistência | `engine/store.go`, `engine/graph_records.go`, `engine/stream_journal.go` | Validadores no carregamento/checkpoint/replay; não basta mudar o handler. |
| Apresentação | `clients/desktop/src/features/chat/ConversationEvents.tsx`, `SessionEvent.tsx` | Consultas agrupadas; ordens devem aparecer na timeline normal. |

Restrições concretas a resolver:

1. `bridgeTools()` exclui o agente geral das tools vinculadas a projeto. Adicionar
   um catálogo próprio e dispatch próprio, sem atribuir um projeto fictício ao geral.
2. O dispatch atual trata nomes fora de `linked_`/`session_` como ferramentas de
   graph. `project_list` e `project_graphs_list` precisam de roteamento explícito,
   autorizado pelo catálogo, antes desse ramo; evitar decidir capacidade só por prefixo.
3. `scheduleLinkedLocked()` acessa diretamente `project(s.ProjectID).Removed` e
   `.Folder`. Uma continuação para o agente geral exige resolução por
   `conversationDirectory()` e checagens de disponibilidade compatíveis com sessão
   sem projeto. Auditar também nomes/contexto inicial e retomada após reinício.
4. `store.go` e `graph_records.go` restringem consultas a endpoints do mesmo projeto.
   Permitir especificamente consultas iniciadas pelo agente geral para sessões
   admitidas, mantendo a regra de mesmo projeto entre sessões normais.
5. A UI remove da timeline normal eventos com `consultationId` de consultas recebidas.
   Esse agrupamento é intencional e deve permanecer para perguntas. Ordens não
   podem ser implementadas como uma variação de texto de `linked_ask`.

Extrair pequenas operações internas dos handlers HTTP quando necessário. Bridge e
HTTP devem compartilhar validação/mutação, sem chamar a própria API por rede,
simular `http.ResponseWriter` ou criar um segundo serviço de gerenciamento.

## 3. Escopo de acesso e contexto

O engine autentica a origem pela bridge instalada e vinculada ao turno; um argumento
`role` ou um sessionId fornecido pelo modelo nunca concede acesso global.

- **Agente geral:** leitura entre projetos não removidos deste engine; controle das
  sessões de conversa admitidas, identificadas por ID estável. Não administra outra
  instalação/host e não altera sua própria identidade singleton.
- **Main/side normais:** preservam descoberta e colaboração no mesmo projeto e as
  instruções atuais sobre menção do usuário/relevância. A nova ordem admite o vínculo
  main/side existente e sessões top-level do mesmo projeto. Não dá controle global,
  tools administrativas ou poder adicional para criar sessões.
- **Nós de graph/subagentes nativos:** não ganham ferramentas de colaboração. Podem
  aparecer como atividade/identidade subordinada nos dados existentes, mas não são
  destinos independentes para ordens ou alterações de configuração desta entrega.

Para descoberta global, listar por padrão sessões top-level; permitir detalhes de
side chats por relação explícita com a sessão principal. Identificar claramente
role/parentId e dados de graph disponíveis, sem representar processo nativo retido
como trabalho em andamento. Sessões/pastas arquivadas são consultáveis; ordens e
consultas exigem restauração pelas regras existentes. Não restaurar implicitamente.

Injetar no começo de cada turno geral um snapshot compacto com horário/revisão,
projetos e contagens de sessões trabalhando/aguardando intervenção. Limitar o tamanho,
indicar truncamento e orientar o uso das tools para detalhes. Não inserir históricos
completos nem disparar turnos quando o snapshot mudar. As consultas retornam o estado
observado no instante da chamada; o agente não promete monitoramento em background.

Atualizar `general-agent.md` para esta capacidade específica, mantendo separado o
prompt das sessões normais. Atualizar `session-collaboration.md` somente para explicar
ordem versus pergunta e seu uso autorizado. Não ampliar os direitos das sessões
normais ao copiar as instruções do agente geral.

## 4. Contratos de leitura propostos

Os nomes abaixo são a interface pretendida. Campos opcionais devem ter semântica
documentada no JSON Schema; IDs/cursors vêm do engine. Retornos limitados, UTF-8
válido, sem caminhos para conteúdo arbitrário no disco. Usar erros claros para
alvo inexistente/inacessível, cursor inválido e catálogo indisponível.

| Tool | Entrada | Retorno/semântica |
|---|---|---|
| `project_list` | `query?`, `cursor?`, `limit?` | ID, nome, diretório cadastrado, pastas e arquivamento; paginação. Não significa teste de conectividade externo. |
| `session_list` | `projectId?`, `folder?`, `query?`, `state?`, `includeArchived?`, `cursor?`, `limit?` | Identidades e resumos recentes, contagens, revisão/horário. Título ambíguo retorna todos os candidatos relevantes. |
| `session_get` | `sessionId` | Resumo detalhado e requisições pendentes; sem carregar todo o histórico. |
| `session_messages_list` | `sessionId`, `cursor?`, `limit?`, `messageId?`, `offset?` | Mensagens user/assistant/agent-authored, origem e estado; recortes de texto longo com continuação. |
| `session_messages_search` | `sessionId`, `query`, `cursor?`, `limit?` | Busca textual simples, sem regex nesta slice; trechos, IDs e cursores para leitura. |
| `session_events_read` | `sessionId`, `cursor?`, `limit?`, `eventId?`, `offset?`, `sinceRevision?` | Eventos de atividade expostos pelo harness, incluindo texto parcial, status, resultados e mudanças em eventos existentes. |
| `session_models_list` | `sessionId` | Catálogo real para o harness/provedor daquele destino, esforços válidos e defaults. |
| `project_graphs_list` | `projectId` | Graphs selecionáveis com ID, nome, disponibilidade e erros de catálogo. Não inicia execução. |

Começar com limites semelhantes aos existentes: listagens até 50 itens, leitura de
mensagens/eventos até 20 itens e teto de bytes por resposta. Definir o teto em uma
constante compartilhada (por exemplo 64 KiB de conteúdo textual), com truncamento e
continuação explícitos. Aplicar o teto também a um único evento grande e a grupos de
consulta; a paginação de UI pode ampliar páginas para preservar agrupamentos e não
deve ser reutilizada sem esse controle. Não fazer um novo índice de busca persistido.

`session_get` inclui ao menos:

- IDs, nome, projeto, pasta visual (`workspace` legado), diretório efetivo de execução,
  role/parentId, arquivamento da sessão ou pasta e timestamps.
- Harness, modelo/esforço configurados e resolvidos, YOLO e graph selecionado.
- Estado bruto e sinais úteis: turno trabalhando, runtime retido, fila, perguntas,
  permissões e graph em andamento. Não equiparar `runtimeActive` a turno trabalhando.
- `usage.context.tokens` e `window`; percentual apenas quando ambos conhecidos e
  janela positiva. Input/output acumulados permanecem separados. Valor ausente é
  desconhecido, nunca zero. Informar instante de observação, sem inventar timestamp
  de medição do harness que o armazenamento não possui.
- Perguntas pendentes completas: ID, itens, opções, múltipla escolha, resposta livre,
  segredo e estado de envio. Permissões podem ser lidas mesmo sem tool de decisão.

Eventos usam a revisão existente em `history.go`. Uma chamada com `sinceRevision`
retorna deltas se ainda disponíveis; caso contrário, retorna `resetRequired` e uma
página limitada para ressincronização. Nunca responder “sem novidades” quando a
revisão expirou. Para deltas paginados, só avançar a revisão consumida quando todas
as mudanças correspondentes tiverem sido entregues. Reinício, troca de filtros e
atualização do mesmo eventId devem ter resultados determinísticos. Não abrir uma
assinatura SSE por tool nem acordar o modelo por token.

## 5. Ordens visíveis e perguntas correlacionadas

### 5.1 Nova tool `session_send`

Usar o mesmo nome tanto no catálogo geral quanto no de sessões normais, com resolução
de destino e escopo distintos. Evitar duas implementações concorrentes de ordem.

Entrada mínima:

```json
{
  "sessionId": "destino-estavel",
  "instruction": "Instrução autocontida dentro do trabalho autorizado",
  "operationId": "chave-estavel-do-envio"
}
```

Contrato:

1. Exigir destino explícito, diferente da própria sessão, conteúdo válido,
   escopo permitido, projeto disponível e destino não arquivado.
2. Persistir uma mensagem normal atribuída ao agente emissor e uma identificação
   durável do envio, atomicamente. `operationId` é único por emissor: repetição da
   mesma requisição devolve o recibo; conteúdo/destino diferente com a mesma chave
   retorna conflito. Pode-se usar um pequeno registro de recibos no modelo atual,
   reutilizando o padrão de `SessionSpawn`, sem criar um sistema de Tasks.
3. Aproveitar `QueuedMessage`, `QueuePayloads`, `Origin` e o scheduler normal. Destino
   ocioso inicia um turno normal; ocupado recebe uma mensagem FIFO para o próximo
   turno. Respeitar capacidade da fila, pause/Stop e falhas de armazenamento. Esta
   primeira versão não força steering nem interrompe o turno corrente.
4. Eventos gerados por esse trabalho NÃO recebem `consultationId`. Ferramentas,
   comandos, progresso e resposta aparecem na timeline normal do destino durante
   a execução, pelas mesmas regras de agrupamento do trabalho iniciado pelo usuário.
5. Retornar `accepted`, `sessionId`, `messageId`, `operationId` e estado observado da
   entrega/fila. Aceitação não afirma execução iniciada ou trabalho concluído.
6. Não esperar a conclusão, não retornar `action=yield`, não criar assinatura de
   progresso nem continuação automática ao emissor. O resultado fica na sessão
   destinatária e pode ser consultado posteriormente dentro do escopo autorizado.
7. Preservar a regra existente de não repetir automaticamente entrega nativa incerta
   após perda de confirmação/reinício. Repetir a tool não adiciona outro trabalho.

`agent_prompt` já diferencia texto de origem agente de evento `user`; manter essa
distinção. Evoluir `SpawnOrigin` de modo compatível ou adicionar metadados opcionais
de origem para distinguir primeira mensagem e instrução posterior. Não reutilizar
o registro de criação de sessão como recibo de envio nem como relação de propriedade.

Na UI, o rótulo atual “Initial prompt from another session” só serve para criação.
Renderizar “Instruction from …” para ordens posteriores, com origem legível. Manter
a distinção também no Session log/exportação e na leitura por tools. Segredos e
perguntas continuam pelos controles existentes.

Autorizações: a instrução é trabalho atribuído pelo emissor, não uma nova mensagem
humana nem concessão de permissões. Registrar origem e, quando disponível, referência
à instrução humana real, sem exigir que um agente invente uma referência local para
trabalho que recebeu de outro agente. Nunca transformar `agent_prompt` em `user` para
satisfazer `graphUserEvent`. As regras atuais que exigem autorização humana específica
para spawn/graph permanecem; propagação de autoridade entre conversas não é parte
deste plano. Se uma operação exigir uma autorização ausente, o destino explicita
essa necessidade ao usuário em vez de fabricar um evento.

### 5.2 Consultas

O agente geral recebe `session_ask(sessionId, topic, question)`, usando o mecanismo
de consultas existente. Sessões normais mantêm `linked_ask`, incluindo o destino
main/side quando omitido. Não expor toda a família `linked_*` ao agente geral por
atalho: ele não tem side agent nem projeto implícito.

Ambas as descrições devem orientar: usar ask para informação/esclarecimento; usar
`session_send` para implementar, modificar arquivos ou executar uma atividade.
Remover “Question or task” da descrição atual de `linked_ask`. Uma consulta pode
precisar de leituras para responder; não tentar classificar semanticamente todo
comando como alteração, nem mudar o agrupamento intencional das consultas.

Preservar `linked_answer` no destinatário, requestId, cancelamento, prevenção de
espera recíproca e o contrato `action=continue`/`yield`. Uma resposta à consulta do
geral pode ser entregue na chamada ou por continuação correlacionada. Garantir que
essa continuação usa o workspace do geral e seu prompt/catálogo, nunca o projeto
consultado como diretório implícito.

Os validadores de consultas e o scheduler devem compartilhar a regra: emissor
geral pode consultar destinos de conversa admitidos; emissores normais continuam
limitados ao mesmo projeto. Não habilitar consultas novas de qualquer sessão para
o geral por simples remoção da igualdade ProjectID. Responder ao requestId já
existente é suficiente para o fluxo solicitado.

## 6. Contratos de controle do agente geral

| Tool | Entrada | Comportamento |
|---|---|---|
| `session_rename` | `sessionId`, `title` | Validação existente de nome, persistência e resumo atualizado. |
| `session_move` | `sessionId`, `folder` | Pasta ativa do mesmo projeto ou Ungrouped; não cria pasta, move arquivos ou altera cwd. |
| `session_settings_update` | `sessionId`, `model?`, `effort?`, `yolo?` | Pelo menos uma alteração; campos omitidos preservados; valores vazios de modelo/esforço seguem defaults existentes. |
| `session_graph_select` | `sessionId`, `graphId` | ID do catálogo do destino; vazio representa None. Somente seleção. |
| `session_question_answer` | `sessionId`, `questionId`, `answers`, `cancelled?` | Responde ao pedido nativo ainda pendente; não envia uma mensagem de chat substituta. |

Configurações: usar o catálogo do destino, nunca o modelo do agente geral. Validar
model+effort como par resultante antes de aplicar. Se a alteração de modelo invalidar
o esforço preservado, devolver erro pedindo combinação válida; não escolher outro
esforço silenciosamente. Resolver catálogo fora do mutex e revalidar sessão/harness/
estado sob lock antes da mutação. Modelo/esforço/YOLO são aplicados entre turnos;
retornar conflito quando a sessão estiver trabalhando. Não fazer Stop para mudar
configuração. Para uma requisição com vários campos, validar todos e aplicar em
uma transação; evitar sucesso parcial entre handlers separados. O runtime retido
deve observar a nova configuração no próximo turno pelo mecanismo já existente.

Reutilizar os limites atuais de seleção de graph e de renomear/mover; não remover
checagens de sessão principal, arquivamento ou execução como efeito colateral.
Retornar estado atualizado para o agente confirmar o que realmente foi aplicado.

Perguntas: reutilizar a validação de `questions.go` para cardinalidade, opções,
texto livre, cancelamento e envio já em progresso. ID expirado ou já respondido
retorna conflito, sem redirecionar a resposta para outra pergunta. Preservar o
roteamento de perguntas de subagentes/nós já projetadas no chat responsável; não
responder diretamente a sessões internas ignorando esse caminho. Conteúdo secreto
mantém o tratamento existente de mascaramento nos registros. Se falta uma preferência
humana necessária, o agente geral pergunta ao usuário antes de responder por ele.

Uma question tool respondida não concede permissões persistentes; YOLO não escolhe
respostas. Não acrescentar tool de aprovação de permissões nesta entrega sem a
aprovação separada da extensão abaixo.

## 7. Extensões propostas, não incluídas automaticamente

O inventário anterior sugeriu capacidades complementares. Elas não foram solicitadas
individualmente pelo usuário e devem permanecer separadas do núcleo acima:

- `session_create`: criação global escolhendo projeto, harness/modelo/esforço/pasta.
- `session_stop`: Stop em sessão selecionada.
- `session_queue_update`: remoção/antecipação/steering de entradas da fila.
- `session_archive` / `session_restore`: arquivar e restaurar sessões.
- `session_permission_reply`: responder às solicitações de permissão.

As ferramentas e controles já disponíveis às sessões normais permanecem. O novo
`session_send` usa a fila como mecanismo interno sem expor administração adicional
da fila. Nesta entrega, arquivadas são legíveis; quando uma ação exige restauração,
o agente informa o estado e o usuário usa o controle existente.

## 8. Sequência de implementação após autorização de início

1. **Capacidades e serviços internos.** Acrescentar catálogo dedicado do geral e
   resolução de destinos por role/ID; ajustar dispatch explícito. Extrair apenas as
   operações usadas dos handlers existentes. Manter bridge privada loopback/token.
2. **Ordens visíveis.** Implementar `session_send` compartilhado, recibo idempotente,
   origem, fila normal e rótulo de UI. Atualizar orientação de ask/ordem em ambos os
   prompts. Essa etapa deve funcionar também entre duas sessões normais do projeto.
3. **Leitura global limitada.** Implementar projetos/sessões, mensagens/busca, eventos,
   contexto e catálogos. Conectar snapshot compacto por turno sem monitoramento.
4. **Consultas a partir do geral.** Reutilizar a máquina de consultas, corrigir os
   caminhos sem ProjectID e ajustar os dois validadores, checkpoint e replay. Manter
   a entrega correlacionada existente e os limites de sessões normais.
5. **Controles solicitados.** Nome/pasta, modelo/esforço/YOLO, graph selecionado e
   respostas às perguntas, com validação compartilhada e conflitos explícitos.
6. **Documentação e verificação focada.** Atualizar `product.md`, README relevante e
   as instruções de colaboração de `AGENTS.md` para registrar a capacidade geral
   específica, preservando que o geral não recebe tools implícitas de projeto.
   Apresentar resultados reais e o que fica para validação humana.

Arquivos novos sugeridos: `engine/general_agent_tools.go` para catálogo/dispatch e
projeções do geral; `engine/session_commands.go` para ordens/recibos, se separar de
`session_collaboration.go` tornar a implementação mais clara. Não criar uma camada
genérica de RPC ou framework de comandos. Os nomes são sugestões, não exigem
fragmentar funções pequenas em muitos arquivos.

## 9. Persistência e compatibilidade

Qualquer novo campo de origem/recibo é opcional para dados anteriores. Não exigir
recriação de sessões, mover workspaces existentes ou regravar históricos como
mensagens humanas. Usar o journal/checkpoint normal e manter validação estrita de
campos/identidades; não relaxar `DisallowUnknownFields` para acomodar novos registros.

Checar `store.go` e `graph_records.go` em conjunto: dados aceitos em runtime devem
carregar e salvar após restart, incluindo consultas cross-project iniciadas pelo
geral. Recibos de ordem precisam continuar disponíveis depois de a mensagem sair
da fila. Campos ou enums novos devem entrar nos caminhos de clone/diff/replay
existentes. Não adicionar schema Tasks ou mecanismo paralelo de persistência.

Compatibilidade pretendida é upgrade do armazenamento anterior para esta versão;
não prometer downgrade para binários antigos que rejeitam campos novos. Fazer
qualquer ensaio de carregamento/replay em fixture/cópia descartável, nunca no banco
release ou dev real. Dados anteriores sem as novas tools continuam utilizáveis.

## 10. Verificação mínima e aceitação manual

Não rodar suítes completas, criar integração complexa, lançar revisores ou duplicar
checks. Após as alterações, um build Go direcionado verifica imports/tipos; se a UI
for alterada, usar o typecheck TypeScript existente. Usar testes unitários pequenos
somente para os invariantes novos que justificam automação: escopo de destinatário,
idempotência/recarga de ordem e validação de consultas gerais. Não usar harness real
para testes automáticos que possam executar ordens em projetos do usuário.

Roteiro de aceitação humana, com sessões/projetos descartáveis:

1. Geral lista dois projetos, suas pastas e sessões ativas/ociosas/arquivadas sem
   misturar pastas visuais com cwd. Títulos duplicados exigem seleção por identidade.
2. Leitura/busca pagina mensagens longas e eventos. Um evento parcial atualizado
   aparece novamente no delta; revisão expirada pede reset sem perda silenciosa.
   Contexto indisponível aparece desconhecido, não 0%.
3. Ordem de geral para sessão ociosa gera turno normal e atividade visível. Ordem
   para ocupada fica na fila; reenvio com mesmo operationId não duplica a atividade.
   Consultas continuam agrupadas como antes e retornam respostas correlacionadas.
4. Uma ordem entre duas sessões normais do mesmo projeto tem a mesma visibilidade.
   Uma tentativa normal de operar outro projeto é recusada. Nó/subagente não ganha
   o catálogo geral por adivinhar nomes/IDs de tools.
5. Consulta iniciada pelo geral e resposta via continuação funcionam sem ProjectID.
   Reload/restart em armazenamento descartável preserva identidades, histórico e
   recibos; entradas incertas não são reenviadas automaticamente.
6. Nome/pasta se atualizam no cliente, cwd permanece igual. Modelos/esforços/YOLO
   válidos mudam entre turnos; combinações inválidas e alvo ocupado retornam conflito
   sem alteração parcial. Selecionar graph não começa uma execução.
7. Resposta a pergunta pendente libera exatamente o pedido correto; resposta
   repetida/expirada é recusada. Opções múltiplas/livres e campos secretos preservam
   o comportamento já oferecido pelo harness.
8. Verificar exposição das tools em Pi, OpenCode e Codex, e visibilidade do mesmo
   histórico em desktop/web/mobile compartilhado. Nenhuma sessão terminada aciona
   automaticamente o agente geral; nenhuma ordem gera notificação de conclusão.

Critério de entrega: núcleo solicitado implementado, checks leves registrados e
limitações de validação humana declaradas. A sessão de implementação deve relatar
arquivos alterados, comandos de verificação e pendências concretas, sem afirmar
sucesso funcional não observado. Commit/push e novas delegações dependem de pedido
do usuário para esta implementação.
