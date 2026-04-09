# Guide: Precise Prefix Cache Aware Routing with Health Scorer

> Based on [llm-d/guides/precise-prefix-cache-aware](https://github.com/llm-d/llm-d/tree/main/guides/precise-prefix-cache-aware), modified to include the **health scorer** plugin from the `ophirazulai/llm-d-inference-scheduler` fork (branch `feat/health-scorer`).

---

## Overview

This guide deploys the precise prefix cache aware routing stack with the addition of the **health scorer** — a two-signal scorer combining KV cache utilization (cubic penalty) and preemption delta detection. The full scorer lineup:

| Scorer | Weight | Purpose |
|--------|--------|---------|
| `precise-prefix-cache-scorer` | 4.0 | Route to endpoints with cached KV blocks |
| `no-hit-lru-scorer` | 0.6 | LRU-based fallback for cache misses |
| `active-request-scorer` | 3.8 | Prefer endpoints with fewer active requests |
| `health-scorer` | 3.5 | Penalize high KV cache pressure and preemption spikes |

---

## Prerequisites

Same as the [upstream guide](https://github.com/llm-d/llm-d/tree/main/guides/precise-prefix-cache-aware#prerequisites):

- [Client tools installed](https://github.com/llm-d/llm-d/tree/main/guides/prereq/client-setup/README.md)
- [Gateway control plane deployed](https://github.com/llm-d/llm-d/tree/main/guides/prereq/gateway-provider/README.md)
- [Monitoring stack installed](https://github.com/llm-d/llm-d/tree/main/docs/monitoring/README.md)
- Namespace created:

  ```bash
  export NAMESPACE=llm-d-health # or any namespace
  kubectl create namespace ${NAMESPACE}
  ```

- [HuggingFace token secret](https://github.com/llm-d/llm-d/tree/main/guides/prereq/client-setup/README.md#huggingface-token) created in the namespace
- [llm-d version chosen](https://github.com/llm-d/llm-d/tree/main/guides/prereq/client-setup/README.md#llm-d-version)

---

## Additional Prerequisites: Fork Dependencies

This guide uses two forked repositories that add `PreemptionCount` metric support:

| Repository | Fork | Branch |
|------------|------|--------|
| `gateway-api-inference-extension` | `ophirazulai/gateway-api-inference-extension` | `feat/preemption-count-metric-v1.4.0` |
| `llm-d-inference-scheduler` | `ophirazulai/llm-d-inference-scheduler` | `feat/health-scorer` |

---

## Step 1: Build and Push the Custom EPP Image

The health scorer plugin lives in the scheduler fork. You need to build a custom container image from it.

```bash
# Clone the scheduler fork (if not already cloned)
git clone -b feat/health-scorer https://github.com/ophirazulai/llm-d-inference-scheduler.git
cd llm-d-inference-scheduler

# Build the EPP image with a custom tag
export IMAGE_REGISTRY=ghcr.io/ophirazulai   # or your own registry
export IMAGE_TAG=health-scorer
make image-build-epp

# Push to registry
make image-push-epp
```

Verify the image is accessible:

```bash
docker pull ${IMAGE_REGISTRY}/llm-d-inference-scheduler:${IMAGE_TAG}
```

> **Note:** If using a private registry, ensure your cluster has pull credentials configured (e.g., `imagePullSecrets`).

---

## Step 2: Clone the Upstream Guide

```bash
git clone https://github.com/llm-d/llm-d.git
cd llm-d/guides/precise-prefix-cache-aware
```

---

## Step 3: Modify the GAIE Values

Edit `gaie-kv-events/values.yaml` to:

1. **Point the EPP image to your fork's build**
2. **Update the EndpointPickerConfig with all four scorers**

Replace the file content with:

```yaml
inferenceExtension:
  replicas: 1
  flags:
    v: 4  # log verbosity
  image:
    ############################
    # Custom image from fork
    ############################
    name: llm-d-inference-scheduler
    hub: ghcr.io/ophirazulai          # <-- your registry
    tag: health-scorer                 # <-- your tag from Step 1
    ############################
    pullPolicy: Always
  extProcPort: 9002
  # ZMQ port for KVEvents subscriber
  extraContainerPorts:
    - name: zmq
      containerPort: 5557
      protocol: TCP
  extraServicePorts:
    - name: zmq
      port: 5557
      targetPort: 5557
      protocol: TCP
  # HuggingFace token for tokenizer
  env:
    - name: HF_TOKEN
      valueFrom:
        secretKeyRef:
          name: llm-d-hf-token
          key: HF_TOKEN
  sidecar:
    enabled: true
    image: ghcr.io/llm-d/llm-d-uds-tokenizer:v0.6.0
    imagePullPolicy: IfNotPresent
    name: tokenizer-uds
    configMap:
      name: tokenizer-uds-config
      data:
        placeholder: ""
    env:
      - name: TOKENIZERS_DIR
        value: /tokenizers
      - name: HF_HOME
        value: /tokenizers
    volumeMounts:
      - mountPath: /tokenizers
        name: tokenizers
      - mountPath: /tmp/tokenizer
        name: tokenizer-uds
  volumes:
    - name: tokenizers
      emptyDir: {}
    - name: tokenizer-uds
      emptyDir: {}
  volumeMounts:
    - mountPath: /tmp/tokenizer
      name: tokenizer-uds

  pluginsConfigFile: "health-scorer-config.yaml"
  pluginsCustomConfig:
    health-scorer-config.yaml: |
      apiVersion: inference.networking.x-k8s.io/v1alpha1
      kind: EndpointPickerConfig
      plugins:
        - type: single-profile-handler
        - type: precise-prefix-cache-scorer
          parameters:
            tokenProcessorConfig:
              blockSize: 64
            indexerConfig:
              tokenizersPoolConfig:
                modelName: Qwen/Qwen3-32B
                local: null
                hf: null
                uds:
                  socketFile: /tmp/tokenizer/tokenizer-uds.socket
            kvEventsConfig:
              topicFilter: "kv@"
              concurrency: 4
              discoverPods: false
              zmqEndpoint: "tcp://*:5557"
        - type: no-hit-lru-scorer
        - type: active-request-scorer
        - type: health-scorer
          parameters:
            kvCacheThreshold: 0.85
            kvWeight: 0.55
            preemptionWeight: 0.45
        - type: max-score-picker
      schedulingProfiles:
        - name: default
          plugins:
            - pluginRef: precise-prefix-cache-scorer
              weight: 4.0
            - pluginRef: no-hit-lru-scorer
              weight: 0.6
            - pluginRef: active-request-scorer
              weight: 3.8
            - pluginRef: health-scorer
              weight: 3.5
            - pluginRef: max-score-picker

  monitoring:
    interval: "10s"
    prometheus:
      enabled: true
      auth:
        secretName: kv-events-gateway-sa-metrics-reader-secret
inferencePool:
  targetPorts:
    - number: 8000
  modelServerType: vllm
  modelServers:
    matchLabels:
      llm-d.ai/inference-serving: "true"
      llm-d.ai/guide: "precise-prefix-cache-aware"
```

### Key Changes from Upstream

| What | Upstream | This Guide |
|------|----------|------------|
| EPP image | `ghcr.io/llm-d/llm-d-inference-scheduler:v0.6.0` | `ghcr.io/ophirazulai/llm-d-inference-scheduler:health-scorer` |
| Config file | `precise-prefix-cache-config.yaml` | `health-scorer-config.yaml` |
| Scorers | `precise-prefix-cache-scorer`, `kv-cache-utilization-scorer`, `queue-scorer` | `precise-prefix-cache-scorer`, `no-hit-lru-scorer`, `active-request-scorer`, `health-scorer` |

---

## Step 4: Deploy

The model service values (`ms-kv-events/values.yaml`) remain unchanged from upstream.

```bash
cd llm-d/guides/precise-prefix-cache-aware
helmfile apply -n ${NAMESPACE}
```

### Gateway Options

```bash
helmfile apply -e agentgateway -n ${NAMESPACE}  # preferred
helmfile apply -e kgateway -n ${NAMESPACE}       # deprecated
```

### Install HTTPRoute

```bash
# For agentgateway, kgateway, or istio:
kubectl apply -f httproute.yaml -n ${NAMESPACE}

# For GKE:
kubectl apply -f httproute.gke.yaml -n ${NAMESPACE}
```

---

## Step 5: Verify the Installation

1. Check all releases are deployed:

   ```bash
   helm list -n ${NAMESPACE}
   ```

2. Check all pods are running:

   ```bash
   kubectl get all -n ${NAMESPACE}
   ```

3. Verify the EPP is using the custom image:

   ```bash
   kubectl get deployment -n ${NAMESPACE} -l app=gaie-kv-events-epp -o jsonpath='{.items[0].spec.template.spec.containers[0].image}'
   # Expected: ghcr.io/ophirazulai/llm-d-inference-scheduler:health-scorer
   ```

4. Verify the health scorer is loaded — check EPP logs for plugin registration:

   ```bash
   kubectl logs -l inferencepool=gaie-kv-events-epp -n ${NAMESPACE} --tail 50 | grep -i "health-scorer"
   ```

---

## Step 6: Test the Health Scorer

1. Port-forward the gateway:

   ```bash
   kubectl port-forward -n ${NAMESPACE} service/infra-kv-events-inference-gateway-istio 8000:80
   ```

2. Send a test request:

   ```bash
   export LONG_TEXT_200_WORDS="Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum."

   curl -s http://localhost:8000/v1/completions \
     -H "Content-Type: application/json" \
     -d '{
       "model": "Qwen/Qwen3-32B",
       "prompt": "'"$LONG_TEXT_200_WORDS"'",
       "max_tokens": 50
     }' | jq
   ```

3. Check health scorer scores in the logs:

   ```bash
   kubectl logs -l inferencepool=gaie-kv-events-epp --all-containers=true -n ${NAMESPACE} --tail 200 | grep "Calculated score" | grep "health-scorer"
   ```

   Expected output (one line per endpoint):

   ```json
   {"level":"Level(-4)","ts":"...","caller":"framework/scheduler_profile.go:165","msg":"Calculated score","plugin":"health-scorer/health-scorer","endpoint":{"name":"ms-kv-events-..."},"score":0.85}
   ```

4. Compare all scorer scores side-by-side:

   ```bash
   kubectl logs -l inferencepool=gaie-kv-events-epp --all-containers=true -n ${NAMESPACE} --tail 200 | grep "Calculated score" | jq -r '[.plugin, .endpoint.name, .score] | @tsv'
   ```

---

## Health Scorer Parameters Reference

| Parameter | Default | Description |
|-----------|---------|-------------|
| `kvCacheThreshold` | `0.85` | KV cache usage fraction above which the KV score is 0 |
| `kvWeight` | `0.55` | Weight of the KV cache signal in the blended score |
| `preemptionWeight` | `0.45` | Weight of the preemption delta signal in the blended score |

### Scoring Formula

**KV cache signal** (cubic penalty):
- `kv >= threshold` → score = 0.0
- `kv < threshold` → score = 1.0 - (kv / threshold)^3

**Preemption signal** (binary delta):
- Preemption count increased since last scoring cycle → score = 0.0
- No increase → score = 1.0

**Final score**: `kvWeight * kvScore + preemptionWeight * preScore`

When metrics are unavailable: fallback score = 0.5

---

## Cleanup

```bash
# From guides/precise-prefix-cache-aware
helmfile destroy -n ${NAMESPACE}

# Or manually:
helm uninstall infra-kv-events -n ${NAMESPACE}
helm uninstall gaie-kv-events -n ${NAMESPACE}
helm uninstall ms-kv-events -n ${NAMESPACE}
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| EPP pod `ImagePullBackOff` | Image not pushed or registry auth missing | Verify `docker pull` works; check `imagePullSecrets` |
| `health-scorer` not in logs | Plugin not registered or config typo | Check `pluginsCustomConfig` spelling matches `health-scorer` exactly |
| All health scores are 0.5 | Metrics not available from vLLM pods | Verify vLLM `/metrics` endpoint is scraped and `KVCacheUsagePercent` is populated |
| Preemption score always 1.0 | `PreemptionCount` not exposed by vLLM scraper | Requires the upstream gateway fork with `PreemptionCount` support |
