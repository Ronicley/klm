# Plano de cadastro de projetos pelo agente geral

## Objetivo

Permitir que o usuário peça ao agente geral para adicionar ao KLM um projeto localizado em uma pasta existente no computador do engine. Exemplo: “Adicione o projeto Acme em C:\\Projects\\Acme”. O projeto deve aparecer no Project Rail e ficar disponível nas tools de inventário e criação de sessões.

Esta entrega é independente de Data Sources. Não inclui criar diretórios, clonar repositórios, inicializar Git, remover/editar projetos pelo agente, instalar dependências, abrir seletores nativos, criar sessões automaticamente ou executar graphs.

## Contrato da ferramenta

Adicionar `project_add` exclusivamente ao catálogo privado do agente geral, com:

- `name`: nome do projeto, usando a validação atual de 1 a 60 caracteres.
- `path`: caminho absoluto de um diretório existente no computador do engine. Este campo representa caminho real, diferentemente de `folder` usado para agrupamento visual de sessões.

Retornar um resultado compacto com `projectId`, `name`, `path` e `outcome` igual a `created`, `existing` ou `restored`. O ID retornado deve funcionar imediatamente em `project_list` e `session_create_options`. Um cadastro novo pode usar o ícone padrão já suportado pela UI; não adicionar upload nem geração de ícone nesta slice.

O prompt dedicado deve exigir solicitação do usuário para cadastrar projetos. Se o caminho não estiver claro, perguntar; nunca inferir o diretório a partir do projeto selecionado, do cliente web/mobile ou do workspace do geral. Um erro de caminho deve permitir ao agente corrigir os argumentos sem cadastrar nada. Registrar um projeto não autoriza criação de sessões, graphs ou operações sobre seus arquivos.

## Reaproveitamento do cadastro existente

A base está em `engine/api.go`: `createProject`, `existingDirectory`, `validIcon`. Atualmente `POST /api/projects` valida nome/pasta/ícone e recupera o ID e agrupamentos de um projeto removido cuja pasta coincide.

Extrair somente os helpers necessários para compartilhar validação e cadastro/restauração com a tool. A tool deve chamar funções internas; não fazer HTTP para a própria API nem simular ResponseWriter. Preservar o contrato HTTP e o comportamento da interface existente.

Para a tool, comparar a pasta normalizada por `filepath.Clean` e as mesmas regras atuais de comparação sem diferenciar maiúsculas no Windows:

1. Se houver um projeto ativo na mesma pasta, retornar `existing` com seus dados atuais, sem duplicar, renomear ou trocar seu ícone.
2. Se somente um cadastro removido corresponder, reutilizar a identidade e preservar sessões/histórico/agrupamentos, seguindo a recuperação existente; retornar `restored`.
3. Caso contrário, cadastrar o projeto e retornar `created`.
4. Se já existirem múltiplos cadastros legados correspondentes, retornar conflito com IDs suficientes para esclarecimento, sem escolher arbitrariamente nem alterar os registros.

A verificação de duplicatas e a mutação devem acontecer sob o mesmo lock. Repetir a tool para uma pasta ativa retorna o mesmo cadastro, inclusive após reiniciar, sem criar uma nova tabela de recibos. A tool não edita um projeto existente. Manter a semântica atual de caminhos; não ampliar esta entrega para deduplicação de todos os aliases, junctions ou links simbólicos.

## Escopo e persistência

Em `engine/general_agent_tools.go`, registrar o schema e fazer dispatch de `project_add` depois da verificação da identidade do geral e antes de exigir um sessionId de destino. Usar `path` próprio nos argumentos, sem reutilizar silenciosamente o campo de pasta visual.

Revalidar turno ativo e contexto sob lock antes da gravação, especialmente após validar o diretório fora do lock. O cadastro deve continuar disponível quando nenhum projeto estiver registrado. Não exigir um projeto de origem nem chamar opções de sessão para cadastrar o primeiro projeto.

Reutilizar `commitLocked` e as notificações existentes para persistir e atualizar clientes. Não escrever diretamente no state.json, mudar permissões, relaxar a bridge privada ou expor a capacidade às sessões normais, side chats, graph nodes ou subagentes. As chamadas continuam atribuídas ao agente; não fabricar eventos humanos ou referências de autorização.

Uma falha de gravação deve retornar erro, sem anunciar cadastro concluído. Preservar compatibilidade dos dados existentes e a separação entre dev e release.

## Arquivos previstos

- `engine/api.go` e, se ajudar a organização, um pequeno arquivo de helpers de projeto.
- `engine/general_agent_tools.go` para schema e dispatch.
- `engine/prompts/general-agent.md` para orientações de uso.
- `product.md`, `AGENTS.md` e `engine/README.md` para documentar a nova capacidade.
- Frontend somente se a atualização existente realmente não apresentar o projeto criado. Não redesenhar a interface.

## Validação e aceitação

Executar apenas uma compilação Go direcionada para um binário temporário e `git diff --check`. Não rodar suites completas nem escrever testes de integração/complexos nesta implementação.

Deixar explícitos os casos para validação manual em ambiente descartável:

- Cadastrar o primeiro projeto com pasta existente; observar o rail e o inventário.
- Repetir o cadastro, inclusive após reiniciar: mesmo ID e nenhum duplicado.
- Nome inválido, caminho relativo, inexistente ou apontando para arquivo: erro sem cadastro.
- Pasta já cadastrada com outro nome: retornar o projeto existente sem alterá-lo.
- Recuperar projeto removido: manter ID e sessões existentes.
- Cadastrar dois pedidos simultâneos para a mesma pasta: um único projeto.
- Usar o projeto retornado para escolher modelos/criar sessão somente quando o usuário solicitar essa ação.
- Confirmar que sessões comuns não recebem a nova tool e que o cadastro manual continua funcionando.

Ao terminar, informar arquivos alterados, verificações efetivamente executadas e limitações. A revisão posterior deve conferir esses contratos e encaminhar achados concretos à mesma sessão implementadora.

## Instruções de entrega

Ler AGENTS.md e product.md antes de editar. Implementar a menor alteração funcional; preservar trabalho alheio, em especial a modificação já presente em `.klm/graphs/generate-installers.yaml`. Não criar agentes/sessões adicionais, não executar graphs, não reiniciar engines em uso e não fazer commit/push. A implementação está autorizada para começar assim que a nova sessão receber este plano.
