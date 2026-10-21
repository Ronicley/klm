# Validação dev e ativação local — 2026-10-16

**Concluído: dev validado, engine/desktop instalados atualizados e verificações pós-reinício aprovadas.** Os registros de preparação abaixo descrevem a sequência executada.

## Resultado confirmado no instalado

- Tarefa de manutenção executada em 19:34:36 local; término registrado em `2026-10-16T22:35:06.931016Z`, `status: success`, `LastTaskResult: 0`, sem próxima execução.
- Antes: **106 eventos**, watermark **116**. Depois: **39 cards**, **6 grupos**, **106 eventos representados**, mesmo watermark.
- Todos os 106 eventos originais preservados; zero expirações durante a ativação. Verificados hashes dos payloads e igualdade dos metadados dos eventos/releases, incluindo IDs, recebimento e TTL. Nenhum replay/backfill no instalado.
- Hashes dos executáveis instalados correspondem aos artefatos staged; engine e frontend HTTP prontos. Rollback não foi necessário; backup de executáveis/prompts anteriores preservado no diretório temporário de validação.
- Conferência direta pelo coordenador após o usuário retornar: API instalada em `7331` confirmou 39 cards/6 grupos, `hasMore: false`, watermark 116.
- Playwright no frontend servido pelo desktop instalado em `7332`: 39 cards; run #264 única, dois jobs + deployment staging success, 21 membros acessíveis em duas páginas, Details carregados; skipped da #263 e failure do job Verify da #267 preservados. Console sem erros ou avisos.
- Redução observada de **106 para 39 cards (aproximadamente 63%)**, preservando cada evento no histórico ou em card individual. Aceite visual pessoal do usuário, inclusive geometria/scroll em todos os clientes, permanece uma validação manual; não foi necessário para confirmar a integridade da ativação.

Pedido do usuário: testar primeiro em dev e aplicar ao engine instalado somente se passar sem falhas.

## Dev — concluído

- Build Go com `-tags dev` e build frontend TypeScript/Vite concluídos. Build emitiu aviso de tamanho de chunks, sem erro de compilação.
- Engine isolado em `17331`, com `KLM_DEV_DATA_DIR` temporário; frontend compilado servido em `17332`. Nenhuma cópia do estado vivo nem credenciais do instalado foi utilizada.
- Snapshot instalado somente leitura: **106 eventos retidos**, watermark **116**, correspondentes às seqs retidas desde 11. O snapshot de 80 eventos da análise inicial já havia crescido.
- Payloads originais lidos pela API e reenviados assinados exclusivamente para uma conexão descartável do dev, preservando ordem de release. Conteúdo bruto não foi gravado em relatório. IDs locais do dev são diferentes; mapeamento por origem e SHA-256 foi guardado nos artefatos temporários.
- Resultado real no dev: **39 cards**, **6 workflows agrupados**, **73 eventos membros**, **33 eventos individuais**, união exata de **106 eventos**, sem omissões ou duplicações. Paginação exercitada com limite 7 tanto em cards como em membros/filhos; Details acessível por todos os IDs originais do dev.
- Resultados individuais preservados: runs #261–263 com Verify success/deploy skipped; #264 com dois jobs e deployment staging success; #267 com estado geral in progress disponível e job Verify failure, sem inferir conclusão da run; #268 success com deploy skipped.
- Playwright no dev: card #264 único, três filhos, histórico completo de 21 membros em duas páginas, Details, bookmark após refresh, skipped de #263, categoria Pipelines (11 cards), busca por job (6 cards) e retorno a All (39 cards). Console: **0 erros, 0 avisos**.
- Reinício do engine dev e nova verificação: mesmos 39 cards/106 eventos/6 grupos, com toda a união preservada. O dev temporário e servidor de preview foram encerrados após a validação.
- Artefatos de validação em `C:\Users\Samuel\AppData\Local\Temp\opencode\feed-grouping-validation`: `check.py`, `baseline.json`, `mapping.json`, `dev-result.json` e dados dev isolados.

## Instalado — preparado; resultado pós-reinício pendente

Agendamento aceito pelo Windows: tarefa `KLM-Feed-Grouping-Activation-20261016`, estado `Ready`, execução única em **2026-10-16 19:34:36 (horário local)**. A tarefa não foi tratada como concluída nesta conversa. Ler `activation-result.json` após o retorno do engine antes de afirmar sucesso.

- Build release Go (`go build -trimpath`) e desktop Tauri `0.5.0` (`npm run tauri -- build --no-bundle -- --locked`) concluídos.
- Ativação local por substituição dos executáveis já instalados e prompts, mantendo os registros/lifecycles existentes; não publica release nem modifica a versão de registro. O engine é parado/iniciado pelo CLI oficial. Não há backfill, replay no instalado, alteração de banco ou reescrita dos eventos.
- Engine alvo: `%LOCALAPPDATA%\Programs\KLM Engine\klm.exe`; desktop: `%LOCALAPPDATA%\KLM Desktop\klm-desktop.exe`.
- Artifacts staged e imutáveis para a operação, com hashes SHA-256:
  - Engine: `aca69f14c11111168d9743a32c803097043d3d35b55985535b97ba68c0eed5b2`.
  - Desktop: `742dc45f8f272edaef5e521113c052b454e69a76dec9a314b17ccd9e0335793c`.
- `activate.py` prepara backup dos executáveis/prompts antigos, captura IDs/metadados e hashes de payload imediatamente antes da atualização, para o engine, substitui os artefatos, inicia engine/desktop e verifica saúde/HTTP do frontend, projeção agrupada, unicidade e preservação dos eventos/metadados/payloads. Em falha após parada tenta restaurar os artefatos anteriores, sem restaurar/reescrever dados do usuário.
- O processo de manutenção precisa ser independente do engine porque seu reinício interrompe a conversa atual. A execução será única, por tarefa agendada no Windows sob o usuário atual. Não há sessão de agente adicional, monitoramento de agentes ou loop permanente.
- Resultado definitivo deve ser lido de `C:\Users\Samuel\AppData\Local\Temp\opencode\feed-grouping-validation\activation-result.json`. `success` registra contagens/hashes e verificação; `rolled-back`/`rollback-failed`/`aborted-before-stop` registram falha. Agendamento não equivale a sucesso da ativação.
- Validação visual final no desktop instalado ainda depende do retorno após reinício.
