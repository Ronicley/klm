# Plano — Dashboard de métricas de sessões

## Objetivo e estado

Criar um dashboard para acompanhar consumo, duração, atividade e resultados das sessões do KLM, usando dados reais do engine e dos harnesses.

O usuário aprovou incluir todas as métricas propostas abaixo no plano. Este documento é planejamento, não implementação nem comprovação de disponibilidade da telemetria. A avaliação humana de qualidade permanece uma evolução futura, separada do resultado técnico da execução.

O KLM apresenta a execução dos harnesses; não deve inventar consumo, cobrança ou sucesso da tarefa. A implementação deve respeitar `product.md`, reutilizar persistência, APIs, notificações e design system existentes e manter os textos da interface em inglês.

## Base existente identificada

- `engine/usage.go`: normalização e persistência de tokens de Pi, OpenCode e Codex; distingue totais de consumo e ocupação de contexto.
- `engine/harness.go`: fluxo de execução e encerramento de turnos.
- `engine/store.go`: estado persistente de sessões.
- `clients/desktop/src/engine.ts`: contratos de sessão e uso; estado atual `idle`, `running` ou `error`.
- `clients/desktop/src/features/chat/SessionStatusBar.tsx`: apresentação existente de tokens.
- `engine/README.md`, seção Token usage details: regras atuais por harness.

Os totais existentes são snapshots acumulados da sessão nativa, não incrementos a somar a cada evento. O contrato de uso consultado ainda não fornece custo ou duração estruturados para este dashboard.

## 1. Definições

### Sessão e execução

- **Sessão:** conversa persistente, com potencialmente vários turnos.
- **Execução:** um turno efetivamente iniciado pelo engine. Mensagens em fila ainda não executadas não contam. Steering no mesmo turno não cria automaticamente outra execução.
- **Estado atual:** trabalhando, aguardando interação ou inativa, derivado do estado real. Contagens de trabalhando e aguardando devem ter uma regra explícita de exclusão para não sugerir totais aditivos quando houver sobreposição.
- **Resultado da execução:** concluída normalmente, falhou, cancelada ou interrompida. Execuções em andamento não têm resultado final.
- **Resultado exibido na sessão:** resultado da última execução, separado do estado atual.

`Completed` significa término técnico normal, não aprovação do objetivo pelo usuário. Idle, Stop, arquivamento e runtime retido não são conclusão de tarefa. Parar o chat não significa parar grafos independentes.

### Tempo

- **Tempo de execução:** tempo decorrido do início ao término do turno, incluindo ferramentas e espera por interação; exclui intervalos entre turnos.
- **Tempo acumulado da sessão:** soma das durações conhecidas de seus turnos.
- Execuções paralelas podem produzir tempo acumulado maior que o tempo de relógio do período. Identificar a métrica como tempo acumulado, não duração do projeto.
- Reinício inesperado não autoriza inventar horário exato de término. Preservar duração observada e marcá-la como parcial quando necessário.

## 2. Catálogo de métricas aprovado para o plano

### Consumo e resultado

| Métrica | Definição / finalidade |
| --- | --- |
| Quantidade de sessões | Sessões únicas no escopo selecionado. |
| Tokens de entrada e saída | Consumo normalizado, com detalhamento de cache/reasoning quando disponível. |
| Tokens totais | Entrada + saída, sem somar novamente categorias já incluídas. |
| Custo | Valor reportado pelo harness ou estimativa identificada, com origem e moeda. |
| Tempo de execução | Duração por execução e soma por sessão. |
| Estado atual | Trabalhando, aguardando interação ou inativa. |
| Resultado da última execução | Concluída, falhou, cancelada ou interrompida. |
| Quantidade de execuções | Turnos efetivamente iniciados dentro da sessão e do recorte. |
| Custo médio por execução | Custo conhecido dividido pelas execuções com custo conhecido, com cobertura visível. Separar estimado e reportado. |
| Sessões trabalhando agora | Sessões com trabalho atual no escopo, sem confundir runtime aberto com trabalho. |
| Sessões esperando você | Sessões com perguntas/permissões pendentes; múltiplos pedidos não multiplicam a sessão. |
| Execuções com erro | Contagem de execuções que falharam. Taxa, se apresentada, usa execuções encerradas como denominador e explicita cancelamentos/interrupções. |
| Última atividade | Momento da última atividade relevante da conversa, sem tratar atualizações do próprio dashboard como atividade. |

### Distribuição e tendências

| Métrica | Definição / finalidade |
| --- | --- |
| Consumo por projeto | Comparar tokens, custo e duração entre projetos, com escopo e cobertura. |
| Consumo por harness e modelo | Atribuir consumo à configuração efetivamente usada, não ao modelo atualmente selecionado. |
| Evolução diária e semanal | Mostrar consumo e execuções por período. |
| Sessões mais caras | Ranking do custo conhecido, identificando valores parciais/estimados. |
| Sessões mais demoradas | Ranking pela soma das durações conhecidas. |
| Consumo de agentes auxiliares | Separar side agents, subagentes e nós de grafos, com atribuição explícita e sem dupla contagem. |

### Demoras e recuperação

| Métrica | Definição / finalidade |
| --- | --- |
| Tempo até a primeira resposta | Início da execução até o primeiro conteúdo de resposta do assistente exposto ao usuário; eventos administrativos não contam. Ausência de resposta fica indisponível, não zero. |
| Tempo esperando sua ação | União dos intervalos com perguntas/permissões pendentes durante a execução, sem somar duas vezes pedidos simultâneos. |
| Tempo em ferramentas | Intervalos de ferramentas/comandos quando o harness fornece início e fim correlacionáveis. Diferenciar soma de durações de ferramentas e tempo de relógio quando houver paralelismo. |
| Quantidade de interrupções | Encerramentos por Stop, substituição ou interrupção detectada, com motivo quando conhecido. |
| Quantidade de novas tentativas | Tentativas com relação explícita à execução anterior; não inferir retry por texto parecido nem tratar toda mensagem posterior como repetição. |

### Qualidade dos dados

- Cobertura por métrica: quantas sessões/execuções têm dados disponíveis, completos ou parciais.
- Identificar valores reportados, estimados e indisponíveis.
- Exibir atualização/staleness quando houver falha de conexão ou refresh.
- Valores ausentes são `null`, nunca zero implícito.
- Total parcialmente conhecido deve ser identificado como parcial.
- Custo desconhecido não significa gratuito; custo reportado/estimado não comprova cobrança financeira de uma assinatura.

## 3. Coleta e persistência

Adicionar registros duráveis por execução usando a infraestrutura existente. Modelo conceitual, a ajustar às estruturas do engine:

```text
ExecutionMetrics
  id, sessionId
  startedAt, endedAt
  outcome, interruptionReason
  durationMs, firstResponseAt
  waitingDurationMs, toolDurationMs
  usage, cost
  coverage, source
  retryOfExecutionId (somente quando explícito)
```

Registrar dimensões efetivas necessárias para agrupamento: projeto, harness, modelo e papel da conversa. Uma execução pode envolver mais de um modelo: preservar divisões por chamada/segmento quando a telemetria permitir; não atribuir tudo ao modelo final.

Regras:

1. Abrir o registro no início real da execução; finalizar no fluxo de settlement.
2. Persistir resultado e métricas sem duplicar registros em reconexão/repetição de eventos.
3. Reconciliar snapshots cumulativos com uma base conhecida; tratar retomadas, reset de contadores e mudança de identidade nativa.
4. Não subtrair totais cegamente quando a base estiver ausente ou tiver mudado.
5. Eventos repetidos de ferramentas/usage devem atualizar o registro identificado, não adicionar consumo novamente.
6. Coleta opcional ausente não deve falhar a execução do usuário.
7. Recuperar execuções abertas após reinício como interrompidas/parciais quando não houver prova de encerramento normal.
8. Preservar o comportamento do status bar e dos controles existentes.

### Custo

Antes de implementar cada adaptador, verificar o que Pi, OpenCode V1/V2 e Codex efetivamente fornecem. Registrar uma matriz de disponibilidade para custo, cache, reasoning, modelo, tempos e identidade de chamadas.

Prioridade: custo nativo reportado. Estimativas só entram quando houver preço e categorias suficientes, com fonte e versão/data da tabela de preços preservadas. Não recalcular silenciosamente o histórico com preços novos. Não usar tokens totais multiplicados pelo preço do modelo atual. Evitar erros monetários de ponto flutuante e não agregar moedas diferentes sem regra explícita de conversão.

Se um harness não fornecer uma métrica, a entrega pode apresentá-la como indisponível com cobertura honesta; não prometer obter dados inexistentes.

## 4. Escopos, períodos e compatibilidade

### Trabalho direto e delegado

- Primeiro corte: sessões principais e consumo direto, explicitamente rotulado.
- Depois: visão de consumo próprio e trabalho delegado, sem misturar ambos no mesmo total.
- Investigar se o harness já inclui subagentes no custo/tokens do pai antes de agregar.
- Relação pai/filho não prova que o consumo é aditivo. Quando a relação contábil for desconhecida, não apresentar total consolidado como completo.
- Sessões independentes criadas por spawn permanecem unidades próprias. Visões agrupadas não podem duplicá-las no total global.
- KLM Agent deve ter categoria própria, sem atribuição a um projeto fictício.
- Grafos independentes mantêm seu próprio ciclo de execução; duração/resultado do chat não os substituem.

### Períodos

- Separar indicadores atuais (trabalhando/aguardando agora) de histórico filtrado por período.
- Usar limites de período explícitos, timestamps UTC e agrupamento diário no fuso escolhido pelo cliente, enviado à API quando necessário.
- Para tendências de consumo, usar a data do consumo reportado quando disponível. Se existir apenas um total por execução, atribuí-lo ao término como aproximação documentada, sem inventar distribuição diária.
- Não atribuir todo o consumo histórico à data de criação da sessão.
- Distinguir totais vitalícios da sessão dos valores dentro do período.

### Histórico e ciclo de vida

- Sessões antigas mantêm os tokens existentes; custo, execução e duração ausentes continuam desconhecidos.
- Não inferir duração por `updatedAt - createdAt` nem criar execuções históricas a partir de suposições.
- Arquivar preserva métricas e não altera resultado. Oferecer inclusão de arquivadas no histórico sem incluí-las indevidamente nos indicadores atuais.
- Seguir as regras de projetos removidos e exclusão permanente de sessões. Métricas não devem criar retenção oculta nem recuperar dados apagados.
- Novos campos devem permitir leitura de registros antigos sem exigir backfill especulativo.

## 5. API e dashboard

Endpoint conceitual: `GET /api/metrics/sessions`, a ajustar aos padrões existentes.

A agregação ocorre no engine, sem enviar o histórico inteiro das conversas ao frontend. Respostas incluem resumo, linhas paginadas, cobertura e instante da atualização. Resumo e tabela usam o mesmo escopo/filtros; o resumo não se limita à página atual.

Filtros previstos:

- Projeto, harness, modelo e resultado.
- Estado atual, papel da conversa e inclusão de arquivadas.
- Período nas entregas que já possuírem histórico de execução confiável.
- Busca por título; ordenação estável por custo, tokens, tempo ou atividade recente.

Interface:

1. Página global **Dashboard**, com filtro por projeto.
2. Cards de sessões, tokens, custo conhecido, duração, execuções, custo médio, trabalhando, aguardando e erros; organizar por grupos para evitar excesso visual.
3. Tabela com sessão, projeto, harness/modelo, estado/último resultado, tokens, custo, duração e última atividade.
4. Clique abre a conversa existente; detalhes mostram execuções e métricas adicionais.
5. Evolução diária/semanal e agrupamentos por projeto/harness/modelo em etapa posterior.
6. Rankings de custo e duração e detalhamento de trabalho delegado.
7. Estados de loading, vazio, erro, desconexão, métricas parciais e indisponíveis.

Reutilizar design system e navegação existentes, mantendo desktop, browser/Focus e WebView mobile compatíveis. Atualizar apenas enquanto visível, usando notificações existentes ou polling leve; não introduzir um novo transporte por padrão.

## 6. Etapas de entrega

### Fase 1 — Contrato e disponibilidade

- Confirmar a semântica deste plano e a matriz real de telemetria por harness/protocolo.
- Fechar a convenção de períodos, custo, atividade e atribuição de descendentes.
- Definir campos mínimos persistidos e compatibilidade com dados antigos.

Aceite: cada métrica tem definição, fonte, unidade, regra de agregação e comportamento quando indisponível.

### Fase 2 — Base durável de execuções

- Registrar início, fim, resultado, duração e origem/configuração efetiva.
- Reutilizar tokens e adicionar coleta de custo disponível.
- Preparar cobertura e recuperação após reinício.

Aceite: vários turnos não sobrescrevem histórico; eventos repetidos não duplicam consumo; dados antigos permanecem legíveis.

### Fase 3 — Primeiro dashboard funcional

- API agregada, cards e tabela paginada.
- Tokens, custo conhecido, duração, última execução, quantidade de execuções, custo médio, trabalhando, aguardando, erros e última atividade.
- Busca, filtros básicos, ordenação e abertura do chat.
- Escopo inicial de consumo direto das sessões principais claramente indicado.

Aceite: usuário consegue identificar sessões caras, demoradas, com falha ou esperando ação e abrir suas conversas.

### Fase 4 — Distribuição e tendências

- Consumo por projeto, harness e modelo.
- Filtros de período, evolução diária/semanal e rankings.
- Garantir consistência entre resumo, tabela e gráficos.

Aceite: números do período são rastreáveis às execuções/consumos correspondentes e não representam totais históricos indevidamente.

### Fase 5 — Demoras, recuperação e agentes auxiliares

- Primeira resposta, espera por ação, tempo em ferramentas, interrupções e retries explicitamente correlacionados.
- Instrumentar o que faltar para novas execuções; não preencher retroativamente sem evidência.
- Separar consumo próprio/delegado e incluir papéis auxiliares e KLM Agent.

Aceite: paralelismo não distorce duração de relógio; nenhuma agregação duplica consumo do pai/filho; limitações reais são representadas por cobertura.

### Fase 6 — Validação humana e documentação

- Check leve de compilação/tipos pertinente às alterações, sem suíte completa por padrão.
- Validar manualmente: execução normal, falha, Stop, múltiplos turnos, perguntas/permissões, ferramentas paralelas, reinício e reconexão.
- Conferir dados reais dos harnesses/protocolos disponíveis, troca de modelo, sessões antigas, arquivamento, exclusão e trabalho delegado.
- Conferir limites de período/fuso e ausência de dados de custo.
- Validar apresentação em desktop, Focus e mobile.
- Registrar o que foi efetivamente verificado e o que permanece pendente; atualizar `product.md` quando houver implementação e decisões efetivamente adotadas.

Não executar suítes completas, revisões adversariais ou criar testes complexos sem pedido explícito.

## 7. Fora desta entrega

- Exportação, alertas de orçamento, limites automáticos de gasto e integrações de faturamento.
- Monitoramento por agente ou execução automática motivada pelas métricas.
- Inferência de qualidade do trabalho a partir de término técnico.
- Avaliação humana como “resultado aprovado”: possível evolução futura, com definição própria antes de implementar.
- Banco analítico, serviço de analytics separado ou infraestrutura especulativa.

## Progresso de implementação

Consolidação em **2026-10-23**: fatias das Fases 1–5 implementadas; **não é conclusão
do plano**. A Fase 6 entregou documentação e checks consolidados de compilação,
mas seu aceite humano continua pendente. Estado detalhado e evidências em
[SESSION_METRICS_IMPLEMENTATION_PROGRESS.md](SESSION_METRICS_IMPLEMENTATION_PROGRESS.md),
contrato em `SESSION_METRICS_TELEMETRY_CONTRACT.md` e notas das Fases 2–5.

- Fase 1: contrato de identidade, categorias, resets, moeda, períodos e não aditividade;
  capacidades nativas não comprovadas continuam indisponíveis.
- Fase 2: registros duráveis de turnos/resultados/duração/modelos observados, recuperação
  parcial após reinício e deltas de tokens Pi com baseline/endpoint correlacionados.
  OpenCode V1/V2 e Codex ainda não têm consumo reconciliado por execução; custo é null.
- Fase 3: Dashboard global, inventário/resumo, filtros, ordenação, paginação, abertura
  de chats e detalhes com consumo observado separado de tokens nativos vitalícios.
- Fase 4 (parcial): período explícito, tabelas diárias/semanais por calendário,
  grupos por projeto/harness registrado e duração recortada. Sem alocação por modelo,
  ranking de custo ou ranking separado de sessões no período.
- Fase 5 (parcial): latência da primeira emissão não vazia do assistente, persistida
  no journal, contagens explícitas de interrupção e papel em novos registros. Sem união
  de espera, intervalos de ferramentas, retries correlacionados ou inventário auxiliar.

A API mantém `GET /api/metrics/sessions` como inventário `all_time` e detalhes
paginados em `/api/metrics/sessions/{id}/executions`. `view=analytics` exige instantes
RFC3339 com offset em `[from,to)`, normalizados para UTC; timezone IANA (UTC padrão),
dias locais e semanas começando segunda-feira. Limites: 1970–9999, até cinco anos e
400 buckets; fronteiras históricas de meia-noite não suportadas são rejeitadas.
Contagens usam início; duração recorta intervalos observados; tokens são atribuídos
uma vez ao fim do turno como aproximação, nunca à criação da sessão. Dados sem fim
conhecido ficam fora dos tokens datados. Filtros selecionam conversas; período não
filtra inventário/atividade atual. Coberturas têm denominadores distintos e explícitos.

Ausência permanece null, zero observado permanece zero e totais parciais têm cobertura.
Custos/médias continuam indisponíveis, inclusive Pi. Não há comprovação de consumo
exclusivo do pai nem soma consolidada com descendentes. Após follow-up, o transporte
Pi `proc.Send` permanece o existente: espera de resposta baseline/final de 2 s após
Send, sem garantia de escrita limitada no tempo ou requisito de deadline no pipe.

Fase 6: exatamente um `go build -tags dev` em `engine`, com binário temporário fora
do repositório removido, e um `npx tsc --noEmit` em `clients/desktop`: ambos exit 0,
sem diagnósticos. Nenhuma correção ou repetição foi necessária nesta consolidação.
Falhas iniciais anteriores (data original não registrada) ficam preservadas como
histórico superado em progress/handoff, não como bloqueio atual. Nenhum teste, suíte,
prompt real, revisão adversarial, commit ou push foi executado nesta etapa.

## Próximo passo

Aceite humano das fatias entregues: Pi real/múltiplos turnos/cache/timeouts/reinício e
status bar, ausência honesta nos outros protocolos, resultados/Stop/steering/fila,
latência e journal, períodos/fusos/DST e coberturas, ciclo de vida, reconexão e UI em
desktop/Focus/mobile. Compilação não comprova esses comportamentos.

Continuam no plano: reconciliação segura OpenCode/Codex, proveniência monetária quando
existir, alocação por modelo, rankings de custo/período, união de espera/ferramentas,
retries explícitos, inventário auxiliar/KLM Agent e contabilidade delegada comprovada.
Não prometer capacidades nativas ainda não verificadas nem preencher histórico por
suposição. O aceite humano da Fase 6 e o plano completo permanecem pendentes.
