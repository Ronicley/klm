# Investigação de latência: chat, streaming, seletores e engine

Data: 2026-09-28. Base: working tree local sobre `4860bc8`, incluindo alterações
preexistentes. Investigação do código atual; não foi verificada a equivalência
entre esse código e o executável instalado em uso no momento dos sintomas.

## Conclusão

Há causas distintas que se amplificam:

1. **SSE por sessão pode esgotar as conexões HTTP/1.1 do navegador.** Sessões com
   runtime retido continuam inscritas mesmo sem um turno ativo.
2. **Toda mutação copia, valida e grava o estado global sob `app.mu`.** O streaming
   tem batching, mas cada batch continua pagando pelo histórico de toda a engine.
3. **O encerramento OpenCode reaplica partes concluídas individualmente.** Cada
   parte gera outra gravação global, inclusive quando o conteúdo não mudou.
4. **O cache de modelos não é compartilhado no frontend e expira de forma
   bloqueante.** O backend serializa todas as descobertas sob um único mutex.
5. **Envio e seleções dependem da confirmação HTTP para atualizar a UI.** Há
   também um problema específico: a resposta HTTP do envio não aplica eventos
   do histórico; a mensagem depende do SSE.
6. **Atualizações de sessões não relacionadas provocam trabalho no frontend.**
   Há recomposição da timeline e renderização de Markdown sem isolamento por
   mensagem. Seu impacto em milissegundos ainda precisa de profiling da UI real.

Os mecanismos 1 e 2 foram exercitados isoladamente. O custo do mecanismo 3 foi
medido pelo caminho de persistência usado por `put`, sem executar um modelo.
Os demais achados foram rastreados no código, não cronometrados em produção.

## 1. Saturação de conexões SSE — prioridade P0

### Evidência

- `clients/desktop/src/App.tsx:268-302`: cria um `EventSource` por sessão em
  execução, por runtime ativo, por grafo ativo, por sessão visível e por certos
  subagentes. Não se limita ao projeto ou chat visível.
- `engine/runtime.go:11,253-303,322-326`: runtimes ficam retidos por 40 minutos
  de ociosidade; workload vivo reinicia a contagem. `runtimeActive` significa
  container retido, não modelo trabalhando.
- `engine/main.go:74,159-166`: API pública serve HTTP sem TLS e sem configuração
  de HTTP/2 cleartext. O caminho direto normal é HTTP/1.1.

No Chromium, as conexões HTTP/1.1 concorrentes para a mesma origem são limitadas.
Streams persistentes competem com GET, PATCH, POST e preflights dessa origem.
Assim, várias sessões já utilizadas podem bloquear a API no navegador, mesmo
quando a engine está disponível. Não são necessárias seis gerações simultâneas.

### Reprodução isolada executada

Servidor Node HTTP/1.1 em loopback, porta de diagnóstico 18439, sem acessar a
engine real. Contexto Chromium separado, seis EventSources que mantêm suas
respostas abertas, seguido de um `fetch('/probe')` de resposta imediata.

Resultado:

```text
openedStreams: 6
pendingWithSixStreams: true
releasedOneAfterMs: 1200
elapsedMs: 1214
protocol: http/1.1
```

A requisição só concluiu depois que um stream foi fechado. O contexto de teste
foi fechado e o servidor foi configurado para encerrar automaticamente.
Isso comprova o mecanismo de saturação, não quantifica quantas conexões havia
no cliente do usuário durante cada incidente. WebView2 e o caminho via proxy
precisam de validação própria; um proxy pode negociar outro protocolo.

### Correção recomendada

Uma conexão multiplexada por cliente para atualizações, com inscrições lógicas
por sessão. Resumos das sessões em background e eventos dos chats observados
devem trafegar por essa conexão. A vida útil do runtime não deve determinar a
quantidade de conexões de rede do frontend.

Remover `runtimeActive` do conjunto de streams reduz o problema, mas não resolve
várias sessões realmente ativas, side chats e subagentes simultâneos.

## 2. Persistência global no caminho crítico — prioridade P0

### Caminho confirmado

```text
delta do harness
  → streamBatch (intervalo nominal de 40 ms)
  → commitStreamUpdates
  → app.mu.Lock
  → JSON marshal + unmarshal de todo diskState
  → aplica a alteração
  → valida registros de grafo
  → JSON marshal de todo diskState
  → escreve arquivo temporário + fsync + replaceFile
  → compara históricos de todas as sessões
  → notifica todos os listeners
  → app.mu.Unlock
```

Referências:

- `engine/stream.go:8,83-108,119-155`.
- `engine/store.go:270-339`.
- `engine/history.go:221-278`: `reflect.DeepEqual` sobre eventos antigos de todas
  as sessões para descobrir o que mudou.

O custo depende do histórico global, incluindo sessões ociosas/arquivadas e
saídas de ferramentas. Várias sessões ativas também aumentam a frequência de
commits concorrendo pelo mesmo mutex. Batching diminui a frequência de gravação,
mas não torna cada gravação proporcional à alteração.

### Medição sintética executada

`engine/latency_investigation_bench_test.go`, Windows/amd64, Ryzen 7 8700G.
Uma sessão ativa com uma pequena alteração e uma sessão não relacionada com
eventos de 8 KiB. Os tamanhos abaixo são de texto histórico, mais overhead JSON.
Três iterações por cenário; médias diagnósticas, não p95 nem latência de produção.

| Cenário | Tempo/operação | Bytes alocados/operação |
| --- | ---: | ---: |
| Commit sem histórico adicional | 5,01 ms | 8.714 |
| Commit com 8 MiB de histórico adicional | 69,11 ms | 47.129.000 |
| Commit com 32 MiB de histórico adicional | 245,88 ms | 161.177.618 |
| Reaplicar 32 partes concluídas, histórico de 8 MiB | 2.011,70 ms | 1.833.770.712 |

Alocação é o total de bytes alocados durante a operação, não memória residente
nem pico de RAM. O último cenário gerou exatamente 32 commits por reconciliação.

Com 8 MiB, um único commit já excedeu o intervalo nominal de batching de 40 ms.
Isso limita a cadência útil de publicação e disputa recursos com envio, steering,
seletores e conclusão do turno. Não há garantia de stream a cada 40 ms.

### Amplificadores

- `commitLocked` acorda os listeners de **todas** as sessões, não apenas das
  alteradas (`store.go:331-338`).
- `api.go:1000-1007` gera e envia atualizações mesmo para sessões não modificadas.
- A revisão global de grafo avança em todos os commits. Os summaries incluem
  projeções de grafo; revisões e objetos mudam mesmo sem mudança nesse grafo.
- `history.go:307` serializa mudanças sob o lock para medir tamanho; a resposta
  SSE é serializada novamente fora dele. Os deltas carregam eventos completos,
  incluindo o texto acumulado, em vez de somente o trecho acrescentado.
- `api.go:169-192` reconstrói resumos/projeções de todas as sessões para o polling
  de estado. O frontend faz esse polling a cada 5 segundos.
- Algumas rotas ainda serializam/escrevem a resposta segurando `app.mu`, por
  exemplo `getConversationGraph` e os handlers de configuração. Um cliente lento
  pode prolongar a seção crítica além da persistência.

### Correção recomendada

Tirar snapshots globais do caminho quente: persistência incremental por
sessão/evento, índices das alterações e publicação apenas dos dados modificados.
Checkpoints globais devem ter uma cadência separada do stream e uma seção crítica
curta. Preservar aceitação durável das mensagens e estados terminais; simplesmente
publicar sucesso antes de uma gravação que pode falhar não resolve o contrato.

Como redução imediata de custo, consolidar a reconciliação em um commit, não
gravar upserts sem alteração e não comparar históricos de sessões intocadas.

## 3. Texto terminou, mas o Stop continua — prioridade P1

### O que o botão representa

`App.tsx:649` usa `session.status === 'running'`. `MessageComposer.tsx:201`
exibe Stop a partir desse estado. Ele não depende de `runtimeActive` nem verifica
se há novos tokens chegando.

`engine/harness.go:312-416` só persiste `idle` após retorno do adapter, fechamento
dos streams/subagentes, conclusão aplicável e aquisição do mutex global.
Receber o último texto não equivale a receber a conclusão nativa do turno.

### OpenCode: cauda de reconciliação real

- `opencode_interactive.go:1219-1317`: após `session.idle`, lê mensagens do turno,
  valida conclusão e reaplica **cada parte** por `putPart`.
- `harness.go:534-556`: uma parte concluída usa flush síncrono; não há comparação
  para evitar persistência de conteúdo idêntico.
- Depois vêm uso de tokens, evento de conclusão e commit de `idle`.

O benchmark das 32 partes exercita esse custo de upsert, não toda a chamada
OpenCode. Uma execução longa com muitas ferramentas multiplica essa cauda.
Além disso, a atualização final pode ficar atrasada no SSE ou na UI.

### Outros harnesses

- Pi aguarda `agent_settled` e pode aguardar estatísticas por até mais 2 segundos
  (`pi_interactive.go:279-292,505-515`). Essa espera ocorre antes do `idle`.
- Codex comum conclui em `turn/completed`; `finishCodexNative` retorna de imediato
  para chats normais (`codex_interactive.go:419-422`). O probe de shutdown/histórico
  desse método é exclusivo de nós de grafo.
- `graphMCPPending` retorna falso para chats comuns (`graph_mcp_calls.go:100-103`).
  Selecionar um grafo não transforma o chat em um nó que aguarda drenagem MCP.

### Playwright

**Não há evidência no código de que um navegador apenas aberto obrigue o chat
normal a continuar em `running`.** A conclusão normal retém o runtime; não espera
todos os seus processos morrerem. Stop cancela o turno e encerra seu container
de processos (`harness.go:240-244`, `platform_windows.go:293-307`), o que explica
um Playwright pertencente ao runtime fechar nesse momento.

Há relação indireta possível: um navegador tratado como workload pode manter o
runtime retido (`runtime.go:278-303`, `platform_windows.go:265-290`), e o frontend
mantém seu SSE por causa de `runtimeActive`. Consumo de CPU/RAM pelo navegador e
espera de uma chamada Playwright ainda ativa são hipóteses distintas, não medidas.

Correção: otimizar o fechamento e medir separadamente último texto, conclusão
nativa, fim da reconciliação, commit de `idle` e recebimento pela UI. Não converter
silêncio no stream em conclusão, pois ferramentas e o harness podem continuar.

## 4. Modelos e esforço — prioridade P1

### Frontend

`features/chat/ModelPicker.tsx:23-44,64-78,105-118`:

- Catálogo em estado local do componente, sem cache compartilhado por projeto/
  harness. `key={session.id}` no App o reinicia ao trocar de sessão.
- Ao abrir modelo **ou esforço** após 120 segundos, incrementa `refresh` e manda
  `?refresh=true`, forçando nova descoberta mesmo se outro componente já atualizou
  o cache da engine.
- Durante revalidação, troca a lista existente por loading e desabilita esforço.
- Cleanup ignora resultados antigos, mas não aborta a requisição em andamento.
- A seleção aguarda `onSave`; o modelo só fecha o menu após confirmação. O slider
  tem preview local, mas trava durante a gravação e o rótulo depende da sessão.

### Engine

`engine/models.go:62-116,130-141,371-488,506-577`:

- Cache por projeto/harness com TTL de 2 minutos, sem retorno stale enquanto
  revalida.
- Um único `catalogMu` é adquirido **antes de consultar o cache**, e fica retido
  durante toda a descoberta. Uma descoberta lenta bloqueia até hits de outras
  chaves.
- Timeout de descoberta de 45 segundos começa depois da aquisição desse mutex;
  o tempo esperando o mutex não está incluído nesse limite.
- OpenCode inicia um servidor temporário para ler providers/config. Pi/Codex
  iniciam processos de controle. Não é uma consulta local trivial de um array.
- `updateModelSettings`, início de execução (`harness.go:277`) e consulta de quota
  (`quota.go:269`) usam o mesmo catálogo. A contenção também pode atrasar o início
  de uma resposta, não só o seletor.

Correção: cache compartilhado no frontend por engine/projeto/harness; mostrar
valor cacheado durante revalidação; deduplicar requisições e cancelar as obsoletas.
No backend, permitir hits independentes de descobertas em outras chaves e
single-flight por chave, sem segurar um lock global durante I/O do harness.

## 5. Envio, fila, steering e seleções otimistas — prioridade P1

### Mensagem fica no input

- `MessageComposer.tsx:90-98,181`: espera `onSend`, mantendo o editor read-only;
  só limpa o rascunho depois da resposta.
- `App.tsx:349-367`: bloqueia operações da sessão enquanto espera a API.
- `api.go:772-892`: prepara anexos e persiste a fila antes de responder.
  Para sessão ociosa, `scheduleMessagesLocked` faz outro commit para promover
  a mensagem ao histórico e iniciar o turno (`message_queue.go:169-208`).
- A resposta HTTP é mesclada com `live=false`. `sessionState.ts:24-40` só aplica
  alterações de eventos de um `SessionUpdate` quando `live=true`. Portanto,
  mesmo recebendo HTTP 202, a mensagem pode continuar ausente do chat até o SSE.

Correção: retirar o rascunho imediatamente para um registro local pendente,
manter o editor disponível e reconciliar por identidade estável com o servidor.
Em falha, preservar o conteúdo recuperável sem sobrescrever um novo rascunho.
Aplicar eventos da resposta de mutação sem avançar indevidamente o cursor SSE.
O endpoint atual gera o ID no servidor: correlação/idempotência precisa ser
definida para não duplicar mensagens ou reenviar entregas incertas.

### Queue não é steering

Enter usa `queue`; durante execução, a mensagem espera o próximo turno. `Send
now` usa `steer` (`MessageComposer.tsx:90,201`). Aceitação pela engine, aceitação
pelo harness e atuação do modelo são etapas diferentes.

O steering acrescenta commits de fila → sending → aceitação no histórico
(`message_queue.go:100-166`). Compartilha a contenção global. No OpenCode há
deadline de confirmação de 15 segundos. O loop também processa eventos que
podem bloquear em persistência. No Codex, frames já disponíveis têm preferência
sobre o canal de steering (`codex_interactive.go:196-245`): um backlog contínuo
pode retardar o despacho. Essa possibilidade não foi cronometrada em execução.

### Stop pode ser ignorado enquanto um envio espera

`updateSession` retorna antes de executar qualquer ação se `pending.current`
contém a sessão (`App.tsx:351`). Stop passa pela mesma função. Logo, durante uma
requisição de mensagem pendente, clicar no Stop visível pode não enviar o POST.
O canal de interrupção deve ser independente do bloqueio de envio/configuração.

### YOLO e grafo

- `SessionOptions.tsx:16-28`: checked vem de `session.yolo`; só muda depois do
  PATCH. Não há preview otimista. Backend apenas valida e persiste a escolha
  (`permission_policy.go:418-448`), sem esperar reiniciar o harness nessa rota.
- `App.tsx:259-267`: o grafo selecionado só muda depois da API. O dropdown já fecha
  imediatamente (`GraphPicker.tsx:29`), mas rótulo/aba dependem da confirmação.
- Grafo já tem cache compartilhado por projeto (`features/graphs/catalog.ts`),
  diferente de modelos; abrir o seletor revalida preservando opções existentes.

Correção: aplicar imediatamente o valor exibido e fazer rollback da operação
correta em caso de falha, protegendo alterações mais novas contra respostas
antigas e polling/SSE atrasado.

**Abrir o menu YOLO é um evento local** (`SessionOptions.tsx:27`, `Menu.tsx:91-98`).
Se o próprio menu/animação demora antes de qualquer seleção, só falta de UI
otimista não explica: é necessário investigar bloqueio de main thread/render.

## 6. Renderização e desconexão aparente

### Renderização: amplificador confirmado no código, custo ainda não medido

- `App.tsx:288-294`: cada SSE chama `setEngine` na raiz.
- `sessionState.ts:30-46`: copia eventos e cria objetos novos mesmo em updates
  vazios. A engine transmite updates de sessões não alteradas após outros commits.
- `ConversationEvents.tsx:180-195,314-330`: refaz agrupamentos/timeline e recria
  os elementos. Componentes de mensagem não estão memoizados.
- `ChatMessage.tsx:52-58`: memoiza a normalização do texto, não a renderização
  completa de `react-markdown`. Mensagens antigas podem ser reprocessadas.
- No bloco de trabalho aberto, detalhes dentro de `<details>` nativo continuam
  montados; há Markdown de reasoning e JSON de dados nativos nesses detalhes
  (`ConversationEvents.tsx:133-169`, `AgentWork.tsx:111-122`).
- Paginação limita a janela inicial, mas mensagens carregadas/recebidas continuam
  no array e não há virtualização. `historyStart` também expande páginas para
  preservar grupos, então o limite nominal de 50 não é um teto rígido.
- A alteração local preexistente em `App.tsx:108-116` acrescenta uma varredura de
  eventos carregados de todas as sessões a cada mudança de `sessions`; não é a
  origem dos gargalos que já existem no código versionado.

Direção: evitar notificações e merges vazios, manter identidade dos objetos
inalterados, isolar a sessão/mensagem modificada e renderizar detalhes pesados
sob demanda. Decidir sobre virtualização após medir a timeline real.

### “Engine disconnected” não prova queda da engine

- `engine.ts:193-205`: GET tem timeout padrão de 15 segundos; timeout, abort e
  falha de rede viram a mesma mensagem de conexão. POST/PATCH não recebem esse
  timeout automático.
- `App.tsx:175-205`: falha no polling `/api/state` ativa o erro global.
- `App.tsx:298-300`: erro em qualquer SSE ativa o mesmo erro global.
- `App.tsx:644-656`: esse erro desabilita controles, ampliando o travamento
  percebido. `onmessage` não limpa o erro; a limpeza normal depende de um polling
  bem-sucedido.

Uma fila de conexões no navegador ou espera pelo mutex pode gerar esse estado
sem encerrar a engine. No caminho HTTP direto, o write timeout geral da engine
é de 20 segundos, portanto operações muito demoradas também podem falhar ao
responder. Corrigir classificação de erro e recuperação, além das causas de atraso.

## Relação com correções anteriores

O histórico Git mostra batching em `7e7d366` e paginação/projeção de histórico
OpenCode em `9e1122c`. Essas proteções continuam presentes. Batching não eliminou
o snapshot global; paginação não eliminou a reaplicação parte a parte no fim.
No começo de cada turno OpenCode, `walkMessages` ainda percorre o histórico todo
para montar baseline/uso (`opencode_interactive.go:631-644`), mesmo com runtime
quente. Paginar limita memória por página, mas não o trabalho total desse início.

Isso estabelece continuidade de causas conhecidas, sem presumir qual correção
específica o usuário quis dizer com “bug anterior”.

## Ordem sugerida de correção

1. **P0 — Transporte:** substituir SSE por sessão por uma conexão multiplexada.
2. **P0 — Persistência:** reduzir a seção crítica e eliminar trabalho global
   proporcional ao histórico a cada delta; começar também pelos commits sem
   mudança e pela reconciliação final em lote.
3. **P1 — Resposta imediata:** envio pendente local, seleções otimistas e Stop
   independente. Essas mudanças podem ser entregues separadamente do armazenamento.
4. **P1 — Catálogo:** cache compartilhado/stale e descoberta concorrente por chave.
5. **P1 — Encerramento:** cauda OpenCode, estatísticas Pi e publicação de conclusão.
6. **P2 — UI e recuperação:** granularidade de renderização, detalhes lazy e
   distinção entre timeout, perda de stream e indisponibilidade da engine.

## Validação restante

Instrumentar durações, IDs e contagens, sem conteúdo de prompts/credenciais:

- Browser: conexões SSE e protocolo; tempo queued/stalled versus TTFB; duração de
  renders e long tasks; tempo clique → feedback local.
- Engine: espera/posse de `app.mu`, bytes do estado, clone, validação, fsync,
  comparação de histórico, listeners acordados e tamanho das respostas.
- Catálogo: hit/stale/miss, chave, espera pelo mutex e duração do processo de
  descoberta.
- Turno: último texto → término nativo → reconciliação → commit idle → UI.
- Input: aceite durável → despacho nativo → confirmação do harness, distinguindo
  queue de steer e sem confundir confirmação com atuação do modelo.

Cenários humanos: uma sessão curta; sessão longa com muitas ferramentas; mais
de seis sessões ativas/retidas; mudança de sessão e modelo após dois minutos;
Playwright aberto mas sem chamada pendente; desktop/WebView2, Focus e acesso via
proxy. Esses cenários reais ainda não foram validados após uma correção, pois
esta entrega é a investigação.

## Artefatos e checks executados

- Reprodução HTTP/1.1/SSE em contexto Chromium isolado, descrita acima.
- Benchmark sintético direcionado, usando armazenamento temporário:

```powershell
# Executar em engine; não inicia harness nem usa dados persistidos do usuário.
go test -run '^$' -bench '^BenchmarkLatencyInvestigation$' -benchtime=3x -count=1 -benchmem
```

Nenhum teste comum foi selecionado por `-run '^$'`; não foi executada a suíte
completa. O benchmark compila o pacote e mede o caminho real de persistência.
`gofmt -d latency_investigation_bench_test.go` terminou sem diferenças após o
ajuste de formatação do diagnóstico. Foi confirmado que o processo do servidor
isolado já havia encerrado. As mudanças de código desta investigação ficam
restritas ao arquivo de benchmark.
