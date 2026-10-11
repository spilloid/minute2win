---
title: API reference
---

# API reference

The console and JSON API share one server. This reference describes the current implementation, not a versioned API stability guarantee.

| Method and path | Purpose | Authorization |
| --- | --- | --- |
| GET /api/manifests | List available profile names | None |
| GET /api/state | Arms, deployments, server time, tokenRequired and degraded | None; report keys are omitted |
| POST /api/armed | Arm a MAC with a profile | Bearer M2W_TOKEN when configured |
| DELETE /api/armed/{mac} | Remove an unused arm | Bearer M2W_TOKEN when configured |
| GET /boot/{mac} | iPXE boot script; consumes an arm | MAC arm; no operator token |
| GET /manifest/{mac} | Manifest for a booting deployment | Short-lived boot delivery window |
| POST /api/report | Engine phase/timing report | Deployment report key |

## Read state

```bash
curl --fail http://localhost:8080/api/state
curl --fail http://localhost:8080/api/manifests
```

## Arm and disarm

**The following POST authorizes erasing the target's selected internal disk when it boots.** Replace the example MAC and profile with the disposable target you have checked. Use the same M2W_TOKEN configured on the server.

```bash
curl --fail -X POST http://localhost:8080/api/armed \
  -H "Authorization: Bearer $M2W_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"mac":"02:00:00:00:00:01","manifest":"lab-win11"}'

curl --fail -X DELETE http://localhost:8080/api/armed/02:00:00:00:00:01 \
  -H "Authorization: Bearer $M2W_TOKEN"
```

A successful DELETE returns 204. If the arm no longer exists it returns 404; if the target already booted, this does not cancel the deployment. Persistence failures return errors and may leave the server degraded, refusing new arms and deploys. Restart discards pending arms.

Do not probe `/boot/{mac}` for an armed target with curl: GET consumes the arm and starts server bookkeeping. It is the deployment entry point, not a health endpoint. Use `/api/state` to inspect the server.

The engine manages report delivery. Manifest delivery is available while booting until the first report or up to ten minutes. A deployment key authenticates reporting but does not authenticate the machine fetching the manifest; see [known issues](STATUS.md).
