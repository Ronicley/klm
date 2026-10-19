# Plano de implementação — biblioteca de skills e conexões MCP

## 1. Objetivo e estado

Permitir que a engine gerencie uma biblioteca de skills e conexões MCP, independente dos harnesses, e disponibilize somente os itens selecionados para cada conversa ou agente de grafo. A engine será o cliente dos servidores MCP externos; os harnesses receberão ferramentas e acesso a recursos através de adapters controlados.

**Estado: plano; implementação e validação de runtime não realizadas.** Este documento consolida decisões do usuário e propõe os contratos técnicos necessários. Escrever este plano não inicia implementação, sessões independentes, graphs, instalações, atualizações de harnesses ou servidores externos.

Ler `AGENTS.md` e `product.md` antes da implementação. Preservar alterações concorrentes existentes. Nomes de estruturas, rotas e novos arquivos abaixo são propostas técnicas, não APIs já implementadas.

## 2. Decisões de produto confirmadas

| Tema | Contrato |
| --- | --- |
| Biblioteca | Compartilhada pelos projetos da engine, acessível em **Settings → Library**. |
| Skills do usuário | Pastas completas em `~/.klm/skills`, na home do usuário do sistema que executa a engine, nunca na home do dispositivo cliente. |
| Adicionar skills | Inicialmente pelo KLM Agent, usando suas ferramentas existentes e uma skill interna exclusiva que ensina a procurar, baixar e instalar no padrão da biblioteca. Sem upload, editor de arquivos ou instalador por formulário nesta entrega. |
| Skills internas | Fontes em `engine/skills`, editáveis externamente como os prompts em `engine/prompts`. |
| Seleção do chat | Modal aberto pelo indicador abaixo do composer; alterações aplicadas no próximo turno completo. |
| Seleção de agentes | Na criação/edição dos agentes em `.klm/agents/*.toml`. |
| Grafos | Capturam seleção e revisões no início real do run; edição posterior não muda esse run. |
| MCP | Engine como cliente; stdio e Streamable HTTP, com autenticação manual e OAuth desde a primeira entrega. |
| Capacidades MCP | Tools, Resources e Elicitation. Prompts e Sampling fora desta entrega. |
| Granularidade MCP | Seleção de conexões e de ferramentas individuais. |
| Herança | Sessões derivadas e side agents novos copiam a seleção aplicada do originador; depois são independentes. Um agente cadastrado fornece sua própria seleção. |
| Atualizações da biblioteca | Automáticas no próximo turno; execuções já iniciadas preservam seu snapshot. |
| Exclusão | Remover das seleções mediante confirmação com impacto explícito; preservar material necessário a execuções em andamento. |
| MCP indisponível | Iniciar com aviso ao usuário e ao agente, sem fingir que a conexão está disponível. |
| Migração | Conversas existentes começam sem itens externos selecionados; não importar configurações globais automaticamente. |
| Compatibilidade | Entregar primeiro nos harnesses com exclusividade validada. Não prometer controle exclusivo nos demais. |
| Pi | Extensão fornecida e carregada pela engine; nenhuma instalação global de plugin MCP. |

Selecionar uma skill a torna disponível para carregamento sob demanda; não injeta necessariamente todo seu conteúdo em cada prompt. Desativar uma skill ou MCP não apaga conteúdo já presente no histórico, nem desfaz efeitos anteriores.

## 3. Base existente e diferenças relevantes

### 3.1 Pontos de integração

- `engine/authoring.go`: `AgentDefinition`, CRUD e validação TOML de agentes; ainda sem seleção de biblioteca.
- `engine/store.go`: sessões, estado persistido e identidade nativa; estender sem perder histórico.
- `engine/graph_snapshot.go`, `graph_contract.go`, `graph_runtime.go`: snapshot de agentes, compilação e execução de nós.
- `engine/runtime.go`: retenção/reutilização de processos; `runtimeToolKey()` considera tools internos e YOLO, mas não revisões de skills/conexões.
- `engine/pi_interactive.go` e `pi-permissions.ts`: extensão materializada por runtime e registro/encaminhamento de tools internos.
- `engine/opencode_interactive.go`, `codex_interactive.go`, `interactive.go`: inicialização e retomada nativa; preservar alterações concorrentes nesses arquivos.
- `engine/linked_bridge.go`: bridge autenticado e loopback-only, com catálogo por papel e dispatch validado. Não é um proxy MCP externo completo: resultados internos são serializados como texto JSON.
- `engine/permission_policy.go`, `graph_mcp_calls.go`: política de permissões e contabilização da finalização de chamadas.
- `engine/session_spawn.go`, caminhos de criação de side chats e ferramentas do general agent: herança de seleção.
- `engine/graph_prompts.go`: carregamento externo dos prompts, resolvido por envio.
- `clients/desktop/src/features/chat/SessionStatusBar.tsx`: indicador atual `3 skills · 2 MCPs`, ausente no general agent e no status bar lateral.
- `clients/desktop/src/features/settings/SettingsDialog.tsx`: entrada da biblioteca.
- `clients/desktop/src/features/agents/AgentEditorDialog.tsx`: seleção inicial dos agentes.
- `distribution/windows/build-engine.ps1` e `engine.nsi`: distribuição atual da pasta `prompts`; incluir `skills` recursivamente.

### 3.2 Contrato atual de mensagens

A investigação anterior nesta conversa descreveu o comportamento antigo de Send now. O `product.md` atual estabelece:

- Enter durante execução envia **steering** na próxima interação nativa.
- **Send now** interrompe o chat e inicia um turno substituto somente após confirmar o encerramento do anterior.
- `queue` explícito e `session_send` mantêm fila para depois da tarefa.

Para esta feature, **turno significa a execução completa iniciada pela engine**, não cada resposta do modelo, chamada de ferramenta ou evento `turn_end` do Pi. Steering mantém o snapshot atual. Um turno substituto usa a seleção mais recente após shutdown confirmado. Não alterar essas semânticas para implementar a biblioteca.

### 3.3 Prompts não estão embutidos hoje

O loader atual procura `KLM_PROMPTS_DIR`, pasta `prompts` ao lado do executável e caminho de desenvolvimento; não há fallback embutido nesse loader. Aplicar o mesmo padrão a skills internas, distribuindo arquivos editáveis. Não assumir que já existe um mecanismo de embedding ou introduzir fallback silencioso para uma edição inválida.

## 4. Arquitetura proposta

```text
~/.klm/skills                 engine/skills ou override externo
       │                              │
       └──── catálogo e revisões ──────┘
                         │
Settings → Library ───────┼──── definições MCP + referências a credenciais
                         │
        seleção do chat / definição do agente
                         │
         snapshot por turno / por run de grafo
                         │
       ┌─────────────────┴────────────────────┐
       │                                      │
materialização de skills            cliente MCP da engine
       │                         stdio / Streamable HTTP / OAuth
       │                                      │
       │                         catálogo e gateway por execução
       │                                      │
       └──────────── adapters ────────────────┘
                    │          │          │
                   Pi       OpenCode    Codex
```

Manter biblioteca, cliente MCP e bridge separados por responsabilidade, sem construir um framework genérico de plugins. Usar uma implementação Go mantida do protocolo MCP, após confirmar cobertura de transporte, OAuth, resources, elicitation e cancelamento. Não implementar o protocolo inteiro manualmente dentro de `linked_bridge.go`.

O endpoint usado pelos harnesses continua privado, autenticado e loopback-only. A biblioteca administrativa usa as APIs normais da engine. Esta entrega não cria um servidor MCP público para outros produtos.

## 5. Biblioteca de skills

### 5.1 Layout, identidade e descoberta

```text
<home-da-engine>/.klm/skills/
  react-performance/
    SKILL.md
    references/
    scripts/
    assets/
```

- Fonte de verdade: filesystem nessa raiz. Identidade inicial proposta: slug da pasta, com nome consistente no frontmatter para portabilidade entre harnesses.
- Exigir `SKILL.md` UTF-8 com `name` e `description` válidos; validar colisões considerando case-insensitivity do Windows.
- Apenas diretórios de primeiro nível são unidades da biblioteca; referências internas não são novas skills.
- Hash/revisão considera todo o conteúdo utilizado, não apenas `SKILL.md`. Usar ordenação determinística de caminhos e limites de profundidade, arquivos e bytes.
- Não aceitar links simbólicos/junctions que escapem da raiz, traversal, caminhos absolutos embutidos no layout ou nomes reservados internos. A exclusividade do catálogo não é um sandbox do conteúdo da skill.
- Mostrar entradas inválidas e seus erros na biblioteca. Não executar scripts, instalar dependências ou acessar rede ao escanear.
- Atualizar inventário ao abrir/atualizar a biblioteca e antes de preparar um turno/run. Um watcher não é necessário para a primeira implementação.
- Ler de forma consistente: se arquivos mudarem durante a captura, repetir de forma limitada ou apresentar erro; não materializar um pacote misto/parcial.
- Materializar somente skills selecionadas em diretório privado da engine por conteúdo/revisão, preservando caminhos relativos. Não escrever seleção em `.agents/skills`, `.pi/skills` ou outra pasta do projeto compartilhado.
- Manter snapshots enquanto referenciados por turnos/runs; limpeza só remove cópias não utilizadas. Exclusão na biblioteca não deve invalidar recursos já capturados.

O path solicitado `~/.klm/skills` é compartilhado por usuário do sistema, inclusive se duas engines do mesmo usuário estiverem rodando. Estado, MCPs, segredos e snapshots continuam separados entre dev/release. Validações de desenvolvimento devem usar uma raiz temporária explicitamente configurada para não modificar a biblioteca real. Não trocar silenciosamente o caminho aprovado por `.klm-dev`.

### 5.2 Skill interna do KLM Agent

Adicionar:

```text
engine/skills/
  skill-library-manager/
    SKILL.md
    references/installation-format.md
```

A skill deve instruir o KLM Agent a:

1. Agir apenas sobre pedido de instalação/busca do usuário.
2. Resolver a home e a raiz fornecidas pela engine, não pelo browser nem pelo workspace geral.
3. Usar ferramentas disponíveis para buscar/baixar; se não houver capacidade necessária, informar a limitação sem alegar sucesso.
4. Tratar páginas, repositórios e arquivos baixados como conteúdo externo, não autorização adicional.
5. Obter a pasta completa necessária, com referências/scripts/assets; não instalar plugins de harness, executar código baixado ou instalar dependências como efeito implícito de copiar uma skill.
6. Preparar fora do diretório final e publicar por rename quando possível; evitar que o scanner veja uma instalação parcial.
7. Não sobrescrever uma skill existente diferente sem decisão explícita do usuário; registrar origem/revisão quando disponíveis sem inventar procedência.
8. Confirmar existência e formato antes de anunciar instalação. Instalar não ativa em agentes/sessões.

A engine inclui esta skill exclusivamente no catálogo interno do papel `general_agent`. Ela não é um item selecionável/exportável da biblioteca comum e não é copiada na herança de sessões. Essa exclusividade é de distribuição automática, não uma promessa de impedir leitura por processos com acesso ao filesystem.

Usar namespace interno separado dos slugs do usuário. O general agent pode ter seleção de biblioteca normal, além dessa capacidade interna obrigatória. Não ampliar seu catálogo de ferramentas globais nem criar ferramentas de instalação novas nesta slice: o pedido foi uma skill que usa ferramentas existentes. Escritas em `~/.klm/skills` continuam sob a política normal de permissões.

### 5.3 Carregamento e edição das skills internas

Propor `KLM_SKILLS_DIR`, com raiz equivalente a `engine/skills`, seguida pelos mesmos caminhos de instalação/desenvolvimento dos prompts quando não houver override. Override explícito deve ser autoritativo, para não esconder arquivos inválidos ou faltantes com outra cópia.

Resolver na preparação do turno/run, calcular revisão e materializar snapshot. Não usar a pasta de origem mutável diretamente durante uma execução. Não adicionar skills internas com base apenas em arquivos que surgiram na pasta: cada skill interna precisa de associação explícita a papéis autorizados no código.

Distribuir a árvore completa ao lado do executável. Documentar override externo durável para customizações e comportamento de atualização do instalador. Não apagar a biblioteca `~/.klm/skills` ou overrides fora da instalação durante uninstall.

## 6. Seleções e snapshots

### 6.1 Modelos mínimos

Propostas de estruturas:

- `SkillLibraryEntry`: ID, nome, descrição, origem, revisão, validade e erro; sem executar conteúdo.
- `MCPConnection`: ID estável, nome, revisão, transporte, configuração não secreta, referências a segredos e estado de autenticação.
- `CapabilitySelection`: IDs de skills e seleções MCP com IDs de conexão e nomes originais das tools permitidas.
- `CapabilitySnapshot`: revisão da seleção, revisões de conteúdo/configuração, skills materializadas, catálogo efetivo, identidade do adapter e diagnósticos de disponibilidade.
- Estado projetado da sessão: seleção desejada, seleção aplicada, alterações pendentes e disponibilidade das conexões. Não persistir tokens transitórios do gateway na timeline.

A seleção é uma allowlist positiva. `[]` significa nenhum item externo; ausência em dados antigos migra para vazio. Selecionar todas as ferramentas na UI salva a lista conhecida naquele momento, não um wildcard que autorize futuras ferramentas silenciosamente. Novas tools de um servidor exigem seleção; revisões de tools já selecionadas são consideradas no próximo turno.

Resources são habilitados no escopo da conexão selecionada nesta primeira versão. Tools de descoberta/leitura de resources são capacidades próprias, não um executor genérico capaz de invocar tools desmarcadas. Não inferir equivalência de permissões por semelhança entre URIs e nomes de ferramentas. Granularidade por recurso não faz parte desta entrega.

Usar IDs em referências, não nomes de exibição, URLs ou caminhos de segredos. Renomear a conexão não altera sua identidade nem nomes técnicos já emitidos. Renomear manualmente pasta de skill equivale inicialmente a remover um ID e adicionar outro; referências quebradas devem aparecer, não ser remapeadas por adivinhação.

### 6.2 Agentes e grafos

Estender a definição TOML; exemplo ilustrativo:

```toml
skills = ["react-performance"]

[[mcp_connections]]
connection_id = "<id-estavel>"
tools = ["search", "get_document"]
```

Manter `.klm/agents` como fonte de verdade. Validar schema, IDs e compatibilidade com o harness efetivo; referências inexistentes produzem diagnóstico, não remoção silenciosa. Arquivos versionados em Git não carregam segredos nem tornam a biblioteca automaticamente portátil para outra engine.

Capturar seleção, revisões e bytes das skills/configurações MCP no início real do run junto do snapshot existente. Para grafos agendados, usar o estado no início, não no agendamento. Agent e Join usam a definição de seu próprio agente; não herdam seleção do orquestrador. Terminal não recebe tools MCP por essa feature.

Revalidar compatibilidade quando um nó sobrescreve o harness. `continue_target` só reutiliza runtime com mesmo snapshot e demais condições nativas existentes. Alterações de biblioteca, agentes ou seleção do chat não modificam o snapshot do run.

Para MCPs, o snapshot congela configuração local e catálogo conhecido, não o comportamento do serviço remoto. Um servidor pode ficar indisponível, revogar token ou mudar implementação. Nunca armazenar tokens vivos como parte do snapshot do grafo. Se não houver catálogo conhecido porque a conexão está offline, registrar essa ausência e avisar; não descobrir e injetar novas tools no meio do run silenciosamente.

### 6.3 Turnos e reutilização

1. No início de uma execução, capturar a revisão desejada e resolver os itens fora do lock de estado.
2. Conectar MCPs com prazo/concurrency limitados, capturar catálogo e aplicar allowlists.
3. Revalidar revisão sob lock antes de assumir o snapshot. Uma edição concorrente não deve produzir configuração híbrida; fazer retry limitado antes do início ou deixar explicitamente pendente para o próximo turno.
4. Incluir snapshot, revisões de schemas/configurações e catálogo de papel na chave de reutilização do runtime.
5. Se a chave mudou, encerrar runtime ocioso e iniciar outro; retomar o mesmo histórico e diretório nativos.
6. Confirmar carregamento/catálogo antes de iniciar a tarefa. Não criar conversa nova silenciosamente se resume falhar.
7. Marcar seleção como aplicada somente após a preparação correspondente, mantendo diagnósticos separados para conexões offline.

Salvar seleção durante execução não inicia tarefa, não reinicia o agente e não altera chamadas em andamento. Steering usa o snapshot atual. Fila de fim de tarefa usa a seleção vigente ao iniciar o próximo turno. Send now usa a nova seleção somente no turno substituto após shutdown confirmado.

Um MCP offline não deve ser anunciado como funcional nem ter chamadas roteadas por configuração antiga. O turno pode continuar com os outros itens e um aviso compacto ao modelo e usuário. Falha de isolamento do adapter, corrupção de snapshot ou skill selecionada inválida são erros distintos: não usar a tolerância a MCP offline para continuar com catálogo inseguro/incoerente.

### 6.4 Herança

- Sessão normal criada manualmente: seleção vazia até escolha do usuário.
- Sessão derivada/side novo: copiar a seleção externa aplicada do originador, incluindo filtros de tools, antes do primeiro turno; não copiar revisão pendente ou skills internas exclusivas.
- Se o originador nunca teve execução aplicada, usar sua seleção salva como configuração inicial, sem alegar que já foi aplicada.
- A cópia referencia IDs da biblioteca, não cria vínculo vivo entre as seleções; futuras revisões de conteúdo seguem a regra normal do próximo turno.
- Conexão offline pode continuar selecionada: não confundir indisponibilidade com remoção da seleção na herança.
- Agente cadastrado: sua seleção é a inicial da execução criada a partir dele.
- Subagentes nativos: validar que não ampliam as capacidades do runtime originador. Não criar modal de configuração separado nem copiar ferramentas privilegiadas de colaboração por acidente.
- Nenhuma regra altera autorizações de criação, consulta entre sessões, graphs ou YOLO.

## 7. Cliente MCP e gateway da engine

### 7.1 Cadastro e lifecycle

Biblioteca permite criar/editar conexões, testar conexão, autenticar/desconectar e excluir com confirmação. Cadastrar não inicia programas nem conecta automaticamente. Test connection é ação explícita e pode executar o servidor local.

Para stdio, guardar comando e argumentos separados, cwd e ambiente explícitos. Usar executável resolvido, sem concatenar shell com dados recebidos; considerar wrappers `.cmd`/runtimes do Windows com escaping controlado. A configuração pode indicar cwd da execução ou diretório fixo; resolver por sessão/nó, não por projeto selecionado na UI. Não instalar runtimes ou pacotes implicitamente. Comandos que baixam pacotes precisam ser explícitos e devem fixar versão.

Começar com clientes/processos por runtime ou ativação, evitando compartilhamento de estado de servidores entre conversas. Credenciais podem ser compartilhadas conforme a conexão cadastrada, mas protocolo, perguntas e chamadas permanecem correlacionados por execução. Otimizar pooling somente depois, se necessário.

Servidores stdio iniciados pela engine precisam de contenção própria e cleanup no Stop, falha de startup, descarte do runtime e shutdown da engine. Não são filhos do harness e não serão terminados automaticamente pelo Job dele. Test connection usa recursos temporários e prazo limitado.

Para Streamable HTTP, implementar handshake, sessões de transporte, respostas JSON/SSE e encerramento suportado pelo servidor. Não confundir SSE do protocolo com suporte ao transporte HTTP+SSE legado. Nenhum daemon externo compartilhado deve ser desligado ao desmarcar uma conexão numa conversa.

### 7.2 Catálogo e dispatch

- Buscar todas as páginas com limites e validar schemas antes de publicar ferramentas.
- Gerar nomes técnicos estáveis compatíveis com limites dos harnesses/provedores a partir de conexão + nome original; armazenar mapeamento reverso sem colisões.
- Reservar namespace dos tools internos e impedir substituição de tools built-in/internos por servidor externo.
- Enviar ao harness apenas o catálogo permitido por papel, execução e seleção.
- No dispatch, validar novamente token, execução ativa, snapshot, conexão, tool/resource e regras aplicáveis. Esconder no catálogo não basta.
- Usar dados JSON para schemas e resultados; não interpolar descrição/schema de servidor como código executável da extensão Pi.
- Tratar descrição, instruções e resultados de servidores como conteúdo externo, sem autoridade para alterar seleção, instalar skills, executar graphs ou criar sessões.
- Não mutar schemas do catálogo do turno em resposta a `list_changed`; invalidar cache e considerar no próximo turno. Se o contrato remoto mudar durante uma chamada, retornar erro, sem reenviar por outro schema automaticamente.

### 7.3 Resultados e Resources

Preservar `isError`, conteúdo estruturado e blocos suportados de resultados; não achatar toda resposta para uma string JSON só porque o bridge interno faz isso hoje. Definir tradução para cada harness e limites de bytes, anexos, itens e contexto. Não descartar silenciosamente tipos não suportados ou dados truncados; expor diagnóstico e leitura paginada/bounded quando aplicável.

Fornecer tools da engine para listar resources e resource templates e ler URIs de uma conexão selecionada. Aceitar URIs como identificadores opacos do servidor; não transformar uma URI em acesso arbitrário ao filesystem ou HTTP da engine. Templates podem exigir argumentos/URI construída pelo agente; o read continua restrito à conexão habilitada. Resultados com resource links podem usar a mesma resolução autorizada.

Não incluir resources completos automaticamente em todo prompt. Não implementar subscriptions de recursos nem espelhar conteúdo de todos os servidores na biblioteca nesta entrega. Imagens/binários exigem tradução suportada ou erro explícito por adapter; não declarar suporte multimodal integral sem validação.

### 7.4 Elicitation

- Reutilizar o modelo de requests pendentes e cards de perguntas, estendendo-o apenas onde forms exigirem tipos não existentes.
- Correlacionar servidor, conexão, request, execução e chamada originadora; resposta retorna ao servidor que perguntou, não como nova mensagem do usuário para o modelo.
- Preservar `accept`, `decline`, `cancel`, campos obrigatórios, tipos e opções conforme a versão negociada. YOLO nunca responde.
- Tratar separadamente elicitation por formulário e por URL nas versões que as suportem. Validar URLs e exigir ação explícita para abrir; não encaminhar segredos por formulários impróprios nem abrir navegação silenciosa.
- Pedidos sem origem roteável ou formas não suportadas recebem resposta explícita compatível; não inventar campos/consentimento.
- Stop, timeout e desconexão encerram o request correspondente; respostas tardias não se aplicam a outro turno.
- Nó de grafo projeta pergunta na conversa proprietária preservando o destino real. Test connection pode usar o diálogo da biblioteca, sem inventar conversa de chat.
- Sampling e Prompts não são anunciados como suportados; métodos recebidos fora da negociação falham explicitamente.

### 7.5 OAuth e segredos

- Usar biblioteca/protocolo MCP OAuth apropriado: descoberta de metadados, Authorization Code com PKCE, state imprevisível, validação de issuer/resource/audience e política de redirects; registro de cliente quando suportado e configuração de client ID quando exigida.
- Guardar segredos/refresh tokens no computador da engine, separados de projeções públicas, snapshots, TOMLs e logs; no Windows, preferir proteção por usuário do sistema. Serializar refresh concorrente para a mesma credencial.
- Não repassar credenciais upstream aos harnesses. Eles recebem somente a capability temporária do gateway da execução.
- Não propagar Authorization para destinos diferentes em redirects. Falhas/diagnósticos precisam de redaction de headers, URLs sensíveis, ambiente e respostas de token.
- Token expirado/revogado torna a conexão indisponível; informar necessidade de autenticar novamente. Não manter token revogado congelado para satisfazer snapshot de grafo.
- OAuth começa por ação humana na Library; reconectar/refresh não concede novas tools automaticamente.
- Desktop, browser remoto e mobile são requisitos: `localhost` no navegador do celular não é a engine. A etapa técnica inicial deve escolher e validar callback alcançável e aceito pelo provedor, ou fluxo de conclusão compatível explicitamente suportado. Não assumir device flow universal nem implementar cópia manual de códigos como bypass do protocolo.
- Não relaxar `loopbackHost` do bridge para receber callbacks externos. Caso seja necessária rota de callback na API normal, ela deve ser dedicada, de uso único, vinculada à transação com expiração/state/PKCE; não transforma o bridge em endpoint LAN.
- A API administrativa continua sob o contrato existente de uso pessoal sem autenticação de rede. Proteger dados de segredos nas respostas não equivale a tornar essa API segura para exposição pública; documentar o limite, sem expandir esta entrega para um sistema de contas.

### 7.6 Permissões, erros e finalização

Preservar política atual: chamadas MCP identificadas e leituras de recursos de skills seguem aprovação automática normal, com negativas explícitas respeitadas; ações de filesystem/shell subsequentes usam suas próprias regras. Não colocar tools externas no bypass privilegiado dos tools internos. A allowlist de capacidades é aplicada mesmo com YOLO.

Servidores locais executados pela engine não herdam automaticamente o sandbox do Codex/Pi/OpenCode. A UI/documentação não deve sugerir isolamento que não existe. A engine controla env, cwd e processos, mas isso não é um sandbox de filesystem/rede.

Requisições têm IDs correlacionados, timeout e cancelamento. Retry de descoberta/leitura de metadados pode ser limitado; não repetir automaticamente `tools/call` após envio com resultado incerto, mesmo que o servidor marque a tool como read-only/idempotent sem outra garantia.

Integrar chamadas do gateway à contabilização de graph activations: bloquear novos dispatches após seal, drenar workers realmente iniciados, distinguir resposta normal de transporte perdido. Encerrar conexão ou matar processo local não é prova de cancelamento remoto. Preservar a regra de finalização incerta e falha do run; uma resposta “job started” encerra a chamada, não comprova conclusão do job.

Registrar apenas um evento lógico de chamada na timeline, evitando duplicar o evento do harness com o do gateway. Manter conexão/tool original, revisão, status e diagnósticos suficientes para recuperação, sem segredos.

## 8. Adapters e gate de compatibilidade

Separar capacidade de biblioteca/exclusividade de outras capacidades existentes de graphs. Aprovação de um graph adapter não comprova controle de skills/MCPs.

### Pi

- Usar `--no-skills` e caminhos explícitos `--skill` para os snapshots selecionados e skills internas autorizadas.
- Continuar `--no-extensions` com extensão própria; não executar `pi install` global nem alterar settings do usuário.
- Estender a extensão para catálogo externo autorizado e tradução de resultados, preservando separação de política e graph gates.
- Confirmar que pacotes, projeto e hooks não reintroduzem skills/servidores externos. Manter providers/modelos/autenticação necessários sem habilitar discovery de recursos proibidos.
- Fazer handshake do catálogo efetivo; falha em extensão deve impedir anúncio de integração pronta, não passar despercebida enquanto o Pi continua.

### OpenCode

- `skills.paths` é aditivo; `OPENCODE_CONFIG_CONTENT`/`OPENCODE_CONFIG_DIR` não isolam sozinhos.
- Avaliar configuração global controlada, `OPENCODE_DISABLE_EXTERNAL_SKILLS`, controle de config do projeto e allowlist nativa de skills, inclusive embutidas.
- Publicar apenas gateway próprio como rota MCP para capacidades gerenciadas; excluir conexões herdadas de fontes global, projeto, ambiente e plugins.
- Preservar explicitamente providers/modelos/auth e instruções de projeto necessárias; não mudar HOME indiscriminadamente nem apagar configs do usuário.
- Não depender de uma mesclagem de arrays que reintroduza caminhos/plugins. Validar o catálogo final, incluindo subagentes e retomada.

### Codex

- `CODEX_HOME` não é isolamento completo: raízes de usuário/projeto/sistema, plugins e skills embutidas precisam ser consideradas.
- Enumerar e desabilitar skills atuais por caminho não é garantia contra descoberta posterior. Não tratar isso como allowlist permanente.
- Investigar recursos disponíveis na versão instalada para substituir/limitar fontes de descoberta e desativar MCPs nativos não gerenciados, preservando auth e resume.
- Confirmar a semântica real de flags/configurações; não presumir que um nome como `skip_host_skill_discovery` desativa todas as fontes em todos os fluxos.
- Sem exclusividade comprovada, marcar o modo gerenciado como não suportado e entregar nos outros harnesses. Não criar fork, ambiente isolado complexo ou atualizar Codex sem nova decisão do usuário.

### Comportamento em harness não suportado

A biblioteca continua acessível, mas não aplicar seleção parcialmente como se fosse exclusiva. Manter seleção salva para futura compatibilidade sem anunciá-la como efetiva; mostrar indisponibilidade e inventário desconhecido onde cabível. Para agentes/runs que exigem o modo gerenciado, preflight recusa harness incompatível. O uso legado do harness, se preservado, deve ficar explicitamente fora da garantia e não receber contagem enganosa de zero itens herdados.

A decisão de entregar por compatibilidade não autoriza um modo misto silencioso. A investigação deve documentar exatamente onde legacy permanece e onde a execução gerenciada é bloqueada antes da UI de lançamento.

## 9. Exclusão, concorrência e migração

### Exclusão confirmada

1. Produzir preview de uso por sessões, side chats, general agent e agentes de todos os projetos conhecidos, incluindo arquivados; runs capturados aparecem como histórico/em andamento, não alvos mutáveis.
2. Confirmar com revisão do item e conjunto de referências; revalidar antes de gravar para evitar remover novas seleções não vistas.
3. Remover das seleções desejadas persistidas e dos TOMLs afetados. Não editar snapshots de runs ou turnos ativos.
4. Registrar mutação recuperável entre arquivos de authoring e estado da engine, reutilizando mecanismos existentes de recovery onde possível. Não alegar transação atômica multiarquivo só porque um lock foi adquirido.
5. Em conflito com edição externa ou arquivo indisponível, retornar erro recuperável, preservar dados necessários e não anunciar exclusão completa. Não sobrescrever mudanças externas.
6. Após confirmação durável, remover/tombstonar item da biblioteca para futuras seleções; preservar cópias de runtime enquanto usadas. Segredos antigos só persistem enquanto necessários ao lifecycle, sem entrar em histórico público.

Remoções/renomes feitos diretamente no filesystem são detectados como itens ausentes; não equivalem à confirmação de remoção em massa pelo produto. Referências de projetos desconhecidos/offline não podem ser reescritas por adivinhação; validar quando voltarem a ser carregadas.

### Migração

- Introduzir campos versionados com seleção externa vazia para sessões e agentes antigos, sem importar nada das instalações globais.
- Não reescrever histórico nativo; skills/resultados anteriormente lidos podem continuar no contexto.
- Em harness validado, descartar runtimes legados na próxima fronteira segura para garantir seleção vazia real.
- General agent recebe a skill interna obrigatória somente pelo seu papel, não como herança global ou item externo da migração.
- Não reescrever snapshots de runs antigos como se sempre tivessem seleção exclusiva. Preservar leitura do histórico e diagnosticar registros legados.
- Informar mudança de comportamento de forma compacta, sem bloquear migração para perguntar seleção.
- Em incompatíveis, não confundir migração do estado para vazio com prova de ausência de recursos nativos herdados.

## 10. Interface e APIs

### Settings → Library

Reutilizar modal/sidebar, componentes e tokens existentes; texto controlado pela aplicação em inglês.

- **Skills**: lista, busca, descrição, validade, origem/revisão quando disponível, uso e exclusão confirmada. Atualização explícita da lista. Sem formulário de instalação/editor nesta slice; acesso ao KLM Agent para o fluxo de instalação sem enviar mensagem automaticamente.
- **MCP connections**: CRUD, transporte, parâmetros, campos de segredo sem eco de valor existente, conexão/autenticação, Test connection, ferramentas descobertas e recursos suportados, uso e exclusão.
- Diferenciar configuração salva, último teste e conexão viva; biblioteca não mantém tudo conectado só para mostrar um indicador verde.
- Não devolver segredos na API; campos devem distinguir manter, substituir e remover.

### Modal da sessão e editor de agente

- Substituir label mockado por botão acessível com contagens reais de seleção externa. Contagem não é indicador de conexão saudável; mostrar pending/unavailable separadamente quando necessário.
- Modal com skills e conexões; cada conexão expande ferramentas selecionáveis, busca e seleção em lote dos itens conhecidos. Não desmarcar tools só porque o servidor está offline.
- Mostrar `Applies next turn` quando seleção desejada difere da aplicada; permitir salvar sem iniciar execução.
- Aplicar ao chat realmente alvo do composer, inclusive overlay do KLM Agent e janelas Focus; nunca editar o geral enquanto o composer aponta para outro chat.
- General agent pode usar a mesma seleção externa, com skill interna apresentada separadamente como gerenciada pelo sistema, sem checkbox de remoção.
- Side chats herdam e possuem estado independente. Preservar o contrato do status bar lateral sem contadores; oferecer acesso à seleção por suas ações/configurações existentes, sem atribuir seleção ao main por engano.
- Editor do agente reutiliza o seletor, salvando TOML; visualização de nó de grafo mostra a seleção capturada/read-only quando houver run.

### APIs propostas

Definir rotas finais durante implementação, mantendo operações distintas:

- Inventário de biblioteca e detalhes paginados/bounded de skills/conexões.
- CRUD de conexões, discovery/test explícito, início/finalização OAuth e desconexão.
- Preview e confirmação de exclusão com controle de revisão.
- Leitura/gravação versionada de seleção por conversa; projeção de seleção aplicada, pending e disponibilidade.
- Campos de seleção no CRUD de agentes e nas projeções de snapshot de grafo.
- Respostas de elicitation pelo fluxo de requests pendentes, correlacionadas à execução de origem.

Persistir com as rotinas existentes de commit e atualizar clientes pelo mecanismo atual de notificações/SSE. Não usar estado local do navegador como fonte de verdade, nem escrever diretamente em `state.json` fora da persistência da engine.

## 11. Sequência de implementação

### Etapa 0 — validar os pontos que podem bloquear a arquitetura

- Registrar versões instaladas e mecanismos reais de exclusividade de cada harness, incluindo config local/global, skills embutidas, plugins, retomada e subagentes.
- Escolher SDK Go MCP com capacidades necessárias e documentar lacunas concretas.
- Definir fluxo OAuth compatível com engine remota/mobile, provedores-alvo e callbacks; demonstrar em ambiente descartável antes de declarar suporte.
- Documentar gate por harness e tratamento de legacy; compatibilidade não é inferida das capabilities de graph atuais.
- Resultado: matriz `suportado / não suportado / não validado`, com evidência e pendências. Começar entrega pelo caminho compatível, sem esperar pelo Codex se ele bloquear.

### Etapa 1 — skills e seleção persistida

- Scanner/validação da biblioteca `~/.klm/skills`, identidades, revisões e snapshots.
- Loader de `engine/skills`, skill interna exclusiva do general agent e distribuição.
- Modelo de seleção desejada/aplicada, migração vazia e herança.
- Pi com seleção exclusiva de skills; captura de agentes/grafos; API mínima e UI funcional de skills.
- Entrega manualmente utilizável sem depender de OAuth ou servidores MCP; não apresentar MCPs como prontos nessa etapa.

### Etapa 2 — cliente MCP e gateway com transporte básico

- Cadastro, segredos, stdio e Streamable HTTP; test/discovery, processos contidos e timeouts.
- Tools filtradas por execução, mapeamento estável, recursos e tradução de resultados.
- Adapters Pi e outros validados, lifecycle, permissões e graph settlement.
- Conexão offline inicia com aviso; nada de retry de operações incertas.

### Etapa 3 — OAuth e Elicitation

- Login/refresh/disconnect e callback remoto/mobile segundo desenho validado na etapa 0.
- Forms/URLs de elicitation negociados, cards e respostas correlacionadas; Stop/timeout/requests tardios.
- Matriz de capacidades de protocolo realmente suportadas, sem anunciar Prompts/Sampling.
- OAuth e Elicitation são partes obrigatórias da entrega final acordada, não backlog opcional só por estarem em etapa posterior.

### Etapa 4 — completar experiência e integração

- Settings → Library completa, seletor por tool e contagens reais.
- Chat, general agent, side, spawn, editor de agentes, snapshots de runs e continuidade.
- Exclusão confirmada recuperável entre seleções/TOMLs e biblioteca; atualizações por turno.
- Expandir adapters apenas conforme evidência de exclusividade; atualizar documentação de produto/instalação.

### Etapa 5 — aceitação humana e registro de limitações

Executar verificações leves pertinentes por etapa, sem suites completas ou reviews adversariais automáticos. Fornecer roteiro manual abaixo, registrando o que foi e não foi exercitado. A feature não está aceita só porque compila.

## 12. Mapa indicativo de alterações

| Área | Arquivos existentes / novos previstos |
| --- | --- |
| Biblioteca de skills | Novos `engine/skill_library.go`, `internal_skills.go`, `engine/skills/skill-library-manager/*`. |
| Seleção/snapshots | Novo `engine/capabilities.go` ou equivalente simples; `store.go`, `runtime.go`, paths de execução/fila. |
| MCP | Novos módulos pequenos de cliente, conexões, gateway, OAuth e elicitation; dependência em `go.mod`. |
| APIs | `api.go`, novos handlers de biblioteca/seleção e projeções públicas sem segredos. |
| Adapters | `pi_interactive.go`, `pi-permissions.ts`, `opencode_interactive.go`, `codex_interactive.go`, `interactive.go`. |
| Bridge/política | `linked_bridge.go`, `permission_policy.go`, `graph_mcp_calls.go` e contenção de processos. |
| Authoring/grafos | `authoring.go`, `graph_snapshot.go`, `graph_contract.go`, `graph_runtime.go`, validação de registros e projeções. |
| Herança | `session_spawn.go`, criação de side chats e caminhos equivalentes do general agent. |
| Frontend | `src/engine.ts`, SettingsDialog, AgentEditorDialog, SessionStatusBar e novos componentes em `features/library`; integrar no composer real, não só no label. |
| Distribuição | `distribution/windows/build-engine.ps1`, `engine.nsi`, pasta `skills` recursiva. |
| Documentação | `product.md`, `AGENTS.md`, `engine/README.md`, `PERMISSIONS.md` e contratos de graphs pertinentes após implementação. |

Não modificar o código nesta fase de planejamento. A lista é orientativa e não exige criar cada arquivo se responsabilidades couberem com clareza nos existentes.

## 13. Validação leve e roteiro de aceitação

Na implementação, usar o menor check pertinente: build Go direcionado quando mudar backend, typecheck TypeScript quando mudar frontend/extension, conforme necessário. Não rodar suites completas, adicionar testes complexos/integrados ou lançar revisores sem pedido explícito. Probes de protocolo devem ser limitados, sem chamadas de modelo/servidores reais ou instalação de pacotes surpresa; credenciais e operações reais requerem ambiente deliberadamente escolhido.

### Roteiro manual

1. Instalar uma skill por pedido ao KLM Agent; confirmar pasta completa em home da engine, catálogo atualizado e nenhuma ativação automática.
2. Skill inválida, colisão, instalação parcial e path escapando da raiz geram diagnóstico sem execução/download pelo scanner.
3. Apenas o general agent recebe `skill-library-manager`; sessões derivadas, side chats, nós e subagentes não a recebem por herança.
4. Editar skill interna externamente e observar nova revisão no próximo turno, sem recompilar; confirmar pasta distribuída no instalador.
5. Sessão vazia em harness suportado não descobre skills/MCPs globais ou locais do projeto. Usar fixtures inofensivas, sem alterar instalações reais para testar.
6. Selecionar skills, MCPs e subset de tools; confirmar catálogo efetivo e bloqueio no gateway para tool desmarcada, inclusive com YOLO.
7. Salvar durante chamada ativa: chamada e steering conservam snapshot; próximo turno usa mudança. Send now confirma shutdown e aplica no substituto.
8. Retomar conversa após recriar runtime: histórico e cwd preservados, capacidades antigas não reaparecem por configuração nativa. Conteúdo já lido permanece no histórico.
9. Conexão HTTP e stdio, tools de mesmo nome em dois servidores, schema inválido, catálogo paginado e resposta grande: comportamento explícito, sem colisão, falso sucesso ou perda silenciosa.
10. Resources: listar, templates e leitura autorizada; URI não vira acesso arbitrário na engine. Prompts e Sampling não anunciados.
11. OAuth no desktop e num cliente remoto/mobile; refresh concorrente, expiração, disconnect, callback inválido e remoção de segredos sem vazamento em UI/logs.
12. Elicitation: aceitar/recusar/cancelar, perguntas em nós de grafo, Stop e resposta tardia; YOLO não escolhe respostas.
13. MCP offline não bloqueia tarefa: usuário/modelo avisados, seleção preservada e sem fallback global. Catálogo novo/reconexão entra somente na fronteira prevista.
14. Duas sessões com seleções distintas e servidor stdio stateful: sem mistura de cwd, perguntas, chamadas ou estado; Stop de uma não derruba outra.
15. Graph com Agent/Join/Fork: snapshot estável após editar biblioteca/agente, skills acessíveis nos worktrees, gate/seal e finalização de chamadas preservados.
16. Remover item em uso: preview correto, confirmação, referências futuras removidas, run atual intacto e recuperação consistente após falha de gravação.
17. Migração de chats/agentes antigos e restart: seleção vazia persistida, sem importação silenciosa, histórico intacto; incompatibilidade exibida honestamente.
18. Confirmar saída dos processos stdio iniciados pela engine em Stop/shutdown e estado incerto de chamadas remotas sem repetição automática.
19. Contagens e modal apontam para sessão correta em main, overlay, Focus e mobile; biblioteca compartilhada, drafts de seleção protegidos contra conflito de revisão.
20. Confirmar dev/release separados em estado/segredos e ausência de escrita de teste na biblioteca real compartilhada.

## 14. Fora de escopo e limites explícitos

- Marketplace, busca própria da engine, upload ZIP/Git pelo formulário, editor de skill na UI e instalação automática de dependências.
- Instalar/atualizar harnesses ou plugins MCP globais, manter fork de harness ou mudar HOME indiscriminadamente.
- MCP Prompts, Sampling, transporte HTTP+SSE legado, subscriptions de resources e promessas de suporte a toda variante/extensão MCP.
- Revogação emergencial global, ferramenta administrativa de biblioteca exposta a todos os agentes, ou aprovação automática de OAuth/elicitation.
- Sandbox completo dos servidores MCP, garantia de cancelamento de jobs remotos ou apagar conhecimento do histórico.
- Pooling sofisticado de servidores, busca dinâmica de tools e controle granular de seleção por resource.
- Redesenho da autenticação de rede da API pública, instalação de túnel ou promessa de callback OAuth universal sem configuração compatível.
- Mudanças nas autorizações de colaboração/graphs, monitoramento automático de sessões, commits/push e execução de graphs para implementar este plano sem autorização separada.

A nova seleção explícita substitui, **no modo gerenciado validado**, o contrato anterior de herdar MCPs configurados nativamente. Atualizar os documentos afetados de forma localizada quando a implementação existir; preservar as garantias de finalização, permissões e propriedade dos processos já aprovadas.
