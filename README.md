# Caixa Interno

Aplicação web para organizar contas internas, registrar entradas e saídas e transferir saldos entre contas. O painel mostra o saldo consolidado e o extrato de lançamentos de cada usuário. O sistema mantém registros de caixa e opera sem integração bancária.

[**Abrir o Caixa Interno**](https://bank-management-api-wy58.onrender.com/)

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/DandaraEmiliano/bank-management-api)

![Painel de caixa com saldo consolidado, contas internas e extrato](docs/painel.png)

## Experimente

1. Crie seu acesso e registre uma conta chamada **Caixa geral** com saldo inicial de R$ 1.000,00.
2. Crie a conta **Reserva** e registre uma transferência de R$ 250,00 a partir de **Caixa geral**.
3. Confira os saldos de R$ 750,00 e R$ 250,00 no painel. O saldo consolidado permanece em R$ 1.000,00.
4. Registre uma saída de R$ 35,50 na reserva para ver o saldo consolidado e o extrato atualizarem juntos.

![Extrato no celular com entrada, saída e transferência](docs/extrato.png)

## Como funciona

O backend em Go usa Gin para servir o painel e a API na mesma origem. O PostgreSQL armazena usuários, contas e lançamentos. O painel recebe saldos e extrato no mesmo snapshot de leitura. Os valores são representados em centavos inteiros, da entrada até o banco.

### Fluxograma do lançamento

![Fluxograma de lançamento com validação, idempotência, transação no PostgreSQL e resposta ao painel](docs/lancamento.svg)

[Código Mermaid do lançamento](docs/lancamento.mmd)

**Concorrência.** O PostgreSQL bloqueia a linha do usuário durante a movimentação. Operações do mesmo usuário são serializadas, enquanto usuários diferentes podem escrever em paralelo. Uma transferência atualiza origem e destino e grava o lançamento em uma única transação. Se uma conta for inválida ou o saldo for insuficiente, a transação reverte todas as alterações.

**Idempotência.** O painel envia `Idempotency-Key` e reutiliza a mesma chave se precisar reenviar a mesma operação. O banco mantém unicidade por usuário. O mesmo conteúdo recebe a resposta do lançamento original. Uma chave reaproveitada com dados diferentes recebe HTTP 409.

**Isolamento.** Consultas e movimentações filtram pelo usuário autenticado. Senhas são armazenadas com bcrypt. A sessão usa JWT com expiração em cookie `HttpOnly` e `SameSite=Strict`, com `Secure` na hospedagem HTTPS. A API também aceita Bearer token.

### Fluxograma de leitura do painel

![Fluxograma de leitura de contas e extrato no mesmo snapshot do PostgreSQL](docs/leitura.svg)

[Código Mermaid da leitura](docs/leitura.mmd)

A leitura usa `REPEATABLE READ` para evitar combinar um saldo anterior à transação com um extrato posterior. O limite do extrato reduz o volume servido em cada atualização.

## Hospedagem

O Blueprint do Render provisiona a aplicação Docker e um PostgreSQL no plano gratuito. O banco do plano gratuito [expira após 30 dias](https://render.com/docs/free#free-postgres). A execução local usa um volume persistente, e o serviço aceita outro PostgreSQL pela variável `DATABASE_URL`.

## Execute localmente

Com Docker e Docker Compose disponíveis, rode na raiz do projeto.

```bash
export JWT_SECRET="$(openssl rand -hex 32)"
docker compose up --build
```

Abra [http://localhost:8080](http://localhost:8080). O volume do Compose preserva o PostgreSQL entre reinícios. Para usar um banco próprio, informe `DATABASE_URL` e `JWT_SECRET` ao executar `go run .`. Defina `COOKIE_SECURE=1` em ambientes HTTPS.

### Compatibilidade

A migração converte a coluna legada `balance` para centavos com arredondamento, preserva a coluna original para auditoria e reverte se houver valores fora do novo intervalo. Os lançamentos passam a ser registrados a partir desta versão. Para clientes antigos, a API continua aceitando `amount` e `balance` como decimais, além de `amount_cents` e `balance_cents`.

## Contrato HTTP

| Método | Rota | Operação |
| --- | --- | --- |
| POST | `/users` | Criar acesso |
| POST | `/login` | Autenticar e iniciar sessão |
| POST | `/logout` | Encerrar a sessão do navegador |
| GET | `/auth/users` | Consultar o próprio perfil |
| GET, POST | `/auth/accounts` | Listar ou criar contas próprias |
| POST | `/auth/accounts/transfer` | Transferir entre duas contas próprias |
| POST | `/auth/accounts/entries` | Registrar uma entrada ou saída de caixa |
| GET | `/auth/overview` | Consultar contas e extrato no mesmo snapshot |
| GET | `/healthz` | Verificar conexão com o PostgreSQL |

## Verificação

O CI valida o código com `go vet` e roda os testes com PostgreSQL e o detector de corridas. A suíte verifica a conversão monetária, o reenvio do mesmo lançamento, a reversão de uma transferência inválida, a concorrência de saídas e o isolamento entre usuários.

```bash
export JWT_SECRET="$(openssl rand -hex 32)"
docker compose up -d --wait postgres
TEST_DATABASE_URL='postgres://bank:bank@localhost:5432/bank?sslmode=disable' go test -race ./...
```
