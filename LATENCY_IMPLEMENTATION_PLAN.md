# Plano de implementação: latência da aplicação

## Objetivo e entrega

Corrigir os gargalos documentados em `LATENCY_INVESTIGATION.md`: saturação SSE,
persistência global no streaming, encerramento lento, catálogo bloqueante, envio
e seleções sem feedback imediato, renderização excessiva e desconexão aparente.

O usuário autorizou uma sessão de implementação Sol High, revisão pelo agente
orquestrador e, após os achados, outra sessão Sol High para correções.
Modelo escolhido a partir do catálogo local: `openai/gpt-6-sol`, harness
`opencode`, esforço `high`.

Esta é uma implementação coordenada de engine e frontend compartilhado. Entregar
fluxos completos e registrar explicitamente qualquer item que permanecer aberto.
Não considerar a correção concluída apenas por melhorar a percepção da UI.

Ler `AGENTS.md`, `product.md` e `LATENCY_INVESTIGATION.md`. O diretório já contém
alterações não commitadas, inclusive colaboração entre sessões e UI do chat.
Preservar e integrar esse trabalho. Não fazer commit/push, gerar instaladores,
reiniciar a engine em uso nem usar dados reais para ensaiar migrações.

## 1. Transporte multiplexado

- Substituir o mapa de EventSources por uma conexão multiplexada por instância
  do frontend. Sessões, side chats e subagentes são inscrições lógicas.
- Receber resumos necessários para sidebar/status de sessões em background;
  carregar eventos apenas das conversas observadas. Runtime retido não justifica
  outra conexão nem a transferência de históricos invisíveis.
- Definir contrato de reconexão, revisão/cursor, reset e alteração de inscrições.
  Uma inscrição nova precisa hidratar seu histórico mesmo se o cursor global
  já tiver avançado; trocas de sessão não podem pular eventos.
- Preservar paginação, consultas vinculadas, subagentes e projeções/permissões de
  grafo. Manter ordenação e tratar gaps com ressincronização limitada.
- Notificar somente dados afetados. Evitar polling redundante ou rajadas de
  refresh de estado decorrentes de cada abertura/reabertura de stream.
- Manter a resolução de endpoints em `platform.ts` e HTTP direto funcional.

**Aceitação:** mais de seis sessões ativas/retidas não criam seis streams no
mesmo cliente nem impedem requests comuns; reconexão e troca de inscrições
preservam o histórico e o estado final.

## 2. Persistência incremental e seção crítica curta

- Remover clone/serialização/gravação de todo o histórico global do caminho de
  cada lote de streaming. Usar a menor representação incremental durável que
  mantenha as garantias atuais; documentar formato e decisão no relatório final.
- Não substituir o gargalo por cópia integral de uma sessão longa a cada token.
- Registrar quais sessões/eventos mudaram, evitar a comparação profunda de todos
  os históricos e preservar identidade dos objetos inalterados.
- Separar checkpoint de estado global da frequência de publicação do stream.
  Serializar gravações de forma ordenada; minimizar a posse de `app.mu` durante
  I/O e manter consistência entre runtime, estado persistido e projeções.
- Evitar commits sem alteração e retirar escrita HTTP de dentro do lock global.
- Tratar recuperação, checkpoint interrompido, journal parcial se aplicável e
  compatibilidade de dados existentes. Aceite de mensagem e estado terminal não
  podem ser confirmados antes de sua durabilidade exigida pelo contrato.
- Preservar atomicidade dos fluxos de fila, consultas e grafos. Migrar todas as
  leituras/gravações necessárias: uma rota antiga não pode sobrescrever dados
  mais recentes mantidos no caminho incremental.

**Aceitação:** o hot path de streaming deixa de escalar com histórico não
relacionado. Reexecutar o benchmark diagnóstico após a alteração; distinguir o
custo do novo hot path do custo residual de checkpoint/commit genérico. Não
melhorar o benchmark trocando por um caminho que produção não usa.

## 3. Encerramento e steering

- Consolidar reconciliação final OpenCode em operações de lote; partes idênticas
  não devem gerar fsync/commit individual. Preservar validação de partes,
  terminalidade, resultado final e ordem dos eventos.
- Reduzir a dependência de telemetria não essencial para publicar término, em
  especial estatísticas Pi, sem misturar respostas de turnos consecutivos nem
  fechar o runtime quente por engano.
- Tratar justiça do despacho de steering, inclusive backlog de frames Codex.
  Diferenciar aceite da engine, confirmação nativa e atuação do modelo.
- Avaliar a reconstrução do baseline OpenCode a cada turno: reduzir leituras
  redundantes quando possível com identificação/cursor confiável, sem inferir
  conclusão por tempo sem tokens ou descartar dados necessários.
- Manter a separação entre término do turno e vida útil do runtime/Playwright.
  Não relaxar barreiras de Choice/MCP nem encerramento de processos de grafos.

**Aceitação:** reaplicar partes já concluídas não produz N commits globais;
`idle` é publicado após término real com mínimo trabalho residual. Stop continua
encerrando os processos que pertencem à sessão.

## 4. Cache de modelo/esforço

- Compartilhar catálogo no frontend por engine/projeto/harness, incluindo
  invalidação quando a configuração efetivamente muda.
- Mostrar catálogo existente durante revalidação. Abrir esforço não deve
  esconder dados válidos nem forçar uma descoberta desnecessária.
- Deduplicar requests e tratar consumidores que desmontam sem cancelar a carga
  necessária a outros consumidores.
- No backend, lock curto para mapa de cache e deduplicação por chave. Nenhum
  lock global de catálogo deve abranger inicialização/processo/I/O do harness.
- Espera por resultado deve respeitar contexto/deadline. Erro ao revalidar não
  destrói catálogo útil; escolha inválida continua sendo rejeitada pelo servidor.

**Aceitação:** troca de sessão no mesmo projeto/harness reutiliza dados;
reabertura após TTL mantém opções usáveis; descoberta de uma chave não bloqueia
hits de outra. A seleção final continua validada pela engine.

## 5. Envio e configurações otimistas

### Mensagens

- Capturar o rascunho enviado, limpar o editor imediatamente e permitir continuar
  digitando. Exibir estado local pendente no chat ou fila conforme modo real.
- Introduzir correlação estável cliente/servidor e tratamento idempotente de
  retransmissão. A mesma identidade com payload incompatível deve falhar; o
  histórico e a fila não podem receber duas cópias da mesma aceitação.
- Reconciliar tanto resposta HTTP quanto SSE, em qualquer ordem, sem pular
  eventos intermediários nem avançar cursor SSE com resposta de uma mutação.
- Distinguir envio pendente, aceito e entrega incerta. Não reenviar automaticamente
  um input possivelmente aceito. Falha preserva conteúdo recuperável sem apagar
  um rascunho mais novo, anexos ou fontes do side chat.
- Preservar FIFO, limite de fila, queue versus steer e os estados de pausa/uncerto
  já definidos em `product.md`.

### Configurações e Stop

- Aplicar imediatamente YOLO, seleção de grafo, modelo e esforço no controle
  correspondente. Implementar rollback/erro ligado à operação correta.
- Proteger a escolha mais nova de responses antigos, polling e snapshots SSE;
  manter estado confirmado separado da intenção pendente quando necessário.
- Respeitar as restrições existentes de mudanças durante execução.
- Stop deve ter caminho independente do bloqueio de envio/configuração. Um POST
  de mensagem pendente não pode fazer o clique em Stop ser descartado.

**Aceitação:** latência artificial de API não mantém o rascunho preso no input;
seleções dão feedback local imediato; falha é recuperável e não perde conteúdo;
HTTP/SSE fora de ordem não duplicam mensagens nem revertem seleções novas.

## 6. Renderização e recuperação

- Evitar updates/merges vazios e manter referências dos eventos inalterados.
- Isolar o trabalho por sessão/mensagem. Markdown antigo não deve ser reprocessado
  só porque outra sessão publicou um delta.
- Montar detalhes pesados sob demanda. Preservar seleção de texto, scroll,
  paginação, favoritos e visual do design system.
- Priorizar correções evidentes de render. Não introduzir virtualização genérica
  ou redesign sem medição que justifique isso.
- Distinguir timeout de request, abort intencional, falha de stream e
  indisponibilidade da engine. Recuperação do stream deve atualizar seu estado
  sem depender exclusivamente de polling bem-sucedido.
- Evitar desabilitar toda a aplicação por falha isolada de uma operação.

**Aceitação:** menu local permanece responsivo durante atividade em background;
falhas transitórias têm estado recuperável e não deixam a UI travada após a
conexão saudável voltar.

## Verificação direcionada e entrega para revisão

O pedido inclui revisão posterior; não autoriza a execução da suíte inteira.
Aplicar a política de `AGENTS.md`: checks focados, sem agentes de revisão nem
delegação adicional pela sessão implementadora.

1. Compilação Go e typecheck/build frontend apropriados, sem checks redundantes.
2. Benchmark de latência usando dados temporários, comparado com a investigação.
3. Verificações pequenas e relevantes para a nova lógica: revisão/reset do
   stream, deduplicação de mensagem, reconciliação HTTP/SSE, rollback e recuperação
   de persistência. Não montar infraestrutura extensa de testes de integração.
4. Checks manuais/sintéticos em ambiente isolado quando necessários; não alterar
   histórico real, iniciar modelos pagos para teste nem parar a engine em uso.
5. Registrar comandos executados, resultados, limites e cenários ainda pendentes
   de validação humana. Não declarar verificados cenários apenas inspecionados.

Criar `LATENCY_IMPLEMENTATION_RESULT.md` contendo:

- checklist dos seis blocos com status e arquivos;
- contratos/formato de persistência, recuperação e compatibilidade;
- contrato do stream e das identidades de mensagem;
- antes/depois dos benchmarks e caminho de produção medido;
- checks executados e limitações;
- riscos concretos ou itens incompletos para a revisão.

O orquestrador revisará correção, concorrência, durabilidade, regressões e
aderência ao escopo. Achados concretos, com arquivos/linhas e reprodução quando
possível, serão o prompt da sessão corretiva Sol High.

## Continuidade da orquestração

Sessões KLM criadas são independentes. O recibo confirma criação/aceitação,
não conclusão. Não fazer polling, aguardar ativamente ou coletar resultados
automaticamente. Quando o usuário trouxer a sessão concluída de implementação,
o orquestrador consultará seu resultado e realizará a revisão solicitada.
Somente depois serão definidos os achados e criada a sessão corretiva.

### Sessão de implementação criada

- Título: `Implementação — latência de chat e engine (Sol High)`.
- ID: `ee8a9371ccd702683d443da496535885`.
- Harness/modelo/esforço: `opencode` / `openai/gpt-6-sol` / `high`.
- Operação: `latency-implementation-sol6-high-a2b62409-v1`.
- Mensagem do usuário que pediu criação:
  `a2b62409e51821fde5681677b8384ccb`.
- Recibo: criação aceita. Início e conclusão da execução não foram inspecionados.
- Próxima etapa do orquestrador: revisar a implementação quando o usuário trouxer
  a conclusão desta sessão; então criar a sessão de correções com os achados.
