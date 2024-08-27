# Note - Kubecon EU 2024 code (DRA code) is now available in the legacy branch

# InstaSlice

InstaSlice works with GPU operator to create mig slices on demand.

# Why InstaSlice

Partitionable accelerators provided by vendors need partition to be created at node boot-time or to change partitions one would have to evict all the workloads at the node level to create new set of partitions.

InstaSlice will help if

 - user does not know all the accelerators partitions needed a priori on every node on the cluster
 - user partition requirements change at the workload level rather than the node level
 - user does not want to learn or use new API to request accelerators  slices
 - user prefers to use stable device plugins APIs for creating partitions

# Demo

[InstaSlice demo](samples/demo_script/demo_video/instaslice.mp4)

## Getting Started

### Prerequisites
- [Go](https://go.dev/doc/install) v1.22.0+
- [Docker](https://docs.docker.com/get-docker/) v17.03+ with [buildx plugin](https://github.com/docker/buildx) for building cross-platform images,
  or [Podman v4.9+](https://podman.io/docs/installation)
- [KinD](https://kind.sigs.k8s.io/docs/user/quick-start/) v0.23.0+
- [Helm](https://helm.sh/docs/intro/install/) v3.0.0+
- [kubectl](https://kubernetes.io/docs/tasks/tools/#kubectl) v1.11.3+

### Install and configure required NVIDIA software on the host

1. Install the [NVIDIA GPU drivers and CUDA toolkit](https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html#driver-installation) on the host.

2. Install the [NVIDIA Container Toolkit (CTK)](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html).

3. Set up the NVIDIA Container Runtime:

    **Note**: The following steps depend on whether you are using Docker or Podman.

  **On Docker**

  - Configure the NVIDIA Container Runtime as the default Docker runtime:

    ```console
    sudo nvidia-ctk runtime configure --runtime=docker --set-as-default
    ```

  - Restart Docker to apply the changes:

    ```console
    sudo systemctl restart docker
    ```

  **On Podman**

  - Generate the [CDI specification file](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html#generating-a-cdi-specification):

    ```console
    sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
    ```

  - Set [NVIDIA container runtime mode](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/cdi-support.html#setting-the-cdi-mode-explicitly) to `cdi`:

    ```console
    sudo nvidia-ctk config --in-place --set nvidia-container-runtime.mode=cdi
    ```

4. Configure the NVIDIA Container Runtime to use volume mounts to select devices to inject into a container:

  ```console
  sudo nvidia-ctk config --set accept-nvidia-visible-devices-as-volume-mounts=true --in-place
  ```

  This sets `accept-nvidia-visible-devices-as-volume-mounts=true` in the `/etc/nvidia-container-runtime/config.toml` file.

### Enable MIG on the GPU

- Check if MIG is enabled on the host GPU - look for `Enabled` in the third row of the table:

```console
# nvidia-smi
Sun Aug 18 09:41:46 2024
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 560.28.03              Driver Version: 560.28.03      CUDA Version: 12.6     |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |
|                                         |                        |               MIG M. |
|=========================================+========================+======================|
|   0  NVIDIA A100-PCIE-40GB          Off |   00000000:07:00.0 Off |                   On |
| N/A   27C    P0             31W /  250W |       1MiB /  40960MiB |     N/A      Default |
|                                         |                        |              Enabled |
+-----------------------------------------+------------------------+----------------------+

+-----------------------------------------------------------------------------------------+
| MIG devices:                                                                            |
+------------------+----------------------------------+-----------+-----------------------+
| GPU  GI  CI  MIG |                     Memory-Usage |        Vol|        Shared         |
|      ID  ID  Dev |                       BAR1-Usage | SM     Unc| CE ENC  DEC  OFA  JPG |
|                  |                                  |        ECC|                       |
|==================+==================================+===========+=======================|
|  No MIG devices found                                                                   |
+-----------------------------------------------------------------------------------------+

+-----------------------------------------------------------------------------------------+
| Processes:                                                                              |
|  GPU   GI   CI        PID   Type   Process name                              GPU Memory |
|        ID   ID                                                               Usage      |
|=========================================================================================|
|  No running processes found                                                             |
+-----------------------------------------------------------------------------------------+
```

- If MIG is disabled, enabled it by running:

```console
nvidia-smi -i <gpu-id> -mig 1
```

Example:

```console
nvidia-smi -i 0 -mig 1
```

**Note**: You may need to reboot the node for the changes to take effect. An asterisk beside MIG status (e.g. `Enabled*`)
means the changes are pending and will be applied after a reboot.

### Install KinD cluster with GPU operator

Create a Kind cluster and install the NVIDIA GPU Operator:

```console
./deploy/setup.sh
```

**Note**: The validator pods `nvidia-cuda-validator-*` and `nvidia-operator-validator-*` of the GPU operator are expected to
fail to initialize. This is because with MIG enabled, but without a MIG partition they effectively have no GPU to run on.

```console
# kubectl get pod -n gpu-operator
NAME                                                          READY   STATUS                  RESTARTS       AGE
gpu-feature-discovery-lzcpv                                   2/2     Running                 0              5m48s
gpu-operator-7b5587d878-vq2gw                                 1/1     Running                 0              6m59s
gpu-operator-node-feature-discovery-gc-8478d46f4c-wvx29       1/1     Running                 0              6m59s
gpu-operator-node-feature-discovery-master-688bb86496-cn97b   1/1     Running                 0              6m59s
gpu-operator-node-feature-discovery-worker-7twxt              1/1     Running                 0              6m52s
nvidia-container-toolkit-daemonset-gpn22                      1/1     Running                 0              6m13s
nvidia-cuda-validator-sjqgk                                   0/1     Init:CrashLoopBackOff   5 (111s ago)   4m54s
nvidia-dcgm-exporter-tlcpv                                    1/1     Running                 0              6m7s
nvidia-device-plugin-daemonset-wbbhx                          2/2     Running                 0              5m53s
nvidia-operator-validator-h7ngh                               0/1     Init:2/4                0              6m10s
```

### Deploy InstaSlice

1. Optionally, build and push custom, up-to-date controller and daemonset images from source:

```console
IMG=<registry>/<controller-image>:<tag> IMG_DMST=<registry>/<daemonset-image>:<tag> make image-build image-push
```

Example:

```console
IMG=quay.io/example/instaslice2-controller:l.0 IMG_DMST=quay.io/example/instaslice2-daemonset:1.0 make image-build image-push
```

**Note**: You can use Podman instead of Docker to build images, just set `CONTAINER_TOOL=podman` before the image-related make targets.

Cross-platform or multi-arch images can be built and pushed using `make image-buildx`. When using Docker as your container tool, make
sure to create a builder instance. Refer to [Multi-platform images](https://docs.docker.com/build/building/multi-platform/)
for documentation on building mutli-platform images with Docker. You can change the destination platform(s) by setting `PLATFORMS`, e.g.:

```console
PLATFORMS=linux/arm64,linux/amd64 make image-buildx
```

2. Deploy the cert manager

```console
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.3/cert-manager.yaml
```

3. Deploy the controller and daemonset with the default images. All required CRDs will be installed by this command:

```console
make deploy
```

or with custom-build images:

```console
IMG=<registry>/<controller-image>:<tag> IMG_DMST=<registry>/<daemonset-image>:<tag> make deploy
```

The all-in-one command for building and deploying InstaSlice:

```console
make image-build image-push deploy
```

Or with custom images:

```console
IMG=<registry>/<controller-image>:<tag> IMG_DMST=<registry>/<daemonset-image>:<tag> make image-build image-push deploy
```

4. Verify that the InstaSlice pods are successfully running:

```console
# kubectl get pod -n instaslicev2-system
NAME                                               READY   STATUS    RESTARTS   AGE
instaslicev2-controller-daemonset-5lbqg            1/1     Running   0          101s
instaslicev2-controller-manager-57b549784c-wkqq2   2/2     Running   0          101s
```

**Note**: If you encounter RBAC errors, you may need to grant yourself cluster-admin privileges or be logged in as admin.

### Run a sample workload

1. Submit a sample workload:

```console
# kubectl apply -f ./samples/test-pod.yaml
pod/cuda-vectoradd-1 created
```

2. check the status of the workload using commands

```console
# kubectl get pods
NAME               READY   STATUS    RESTARTS   AGE
cuda-vectoradd-1   1/1     Running   0          15s
```

and

```console
# kubectl logs cuda-vectoradd-1
GPU 0: NVIDIA A100-PCIE-40GB (UUID: GPU-1785aa6b-6edf-f58e-2e29-f6ccd30f306f)
  MIG 1g.5gb      Device  0: (UUID: MIG-2cc7f78c-04eb-5a3c-92c7-f423e3572bb8)
[Vector addition of 50000 elements]
Copy input data from the host memory to the CUDA device
CUDA kernel launch with 196 blocks of 256 threads
Copy output data from the CUDA device to the host memory
Test PASSED
Done
```

While the pod is running, you can observe the MIG slice created for it automatically:

```console
# nvidia-smi
Sun Aug 18 11:48:20 2024
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 560.28.03              Driver Version: 560.28.03      CUDA Version: 12.6     |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |
|                                         |                        |               MIG M. |
|=========================================+========================+======================|
|   0  NVIDIA A100-PCIE-40GB          Off |   00000000:07:00.0 Off |                   On |
| N/A   32C    P0             63W /  250W |      13MiB /  40960MiB |     N/A      Default |
|                                         |                        |              Enabled |
+-----------------------------------------+------------------------+----------------------+

+-----------------------------------------------------------------------------------------+
| MIG devices:                                                                            |
+------------------+----------------------------------+-----------+-----------------------+
| GPU  GI  CI  MIG |                     Memory-Usage |        Vol|        Shared         |
|      ID  ID  Dev |                       BAR1-Usage | SM     Unc| CE ENC  DEC  OFA  JPG |
|                  |                                  |        ECC|                       |
|==================+==================================+===========+=======================|
|  0   11   0   0  |              13MiB /  4864MiB    | 14      0 |  1   0    0    0    0 |
|                  |                 0MiB /  8191MiB  |           |                       |
+------------------+----------------------------------+-----------+-----------------------+
...
```

3. Delete the sample pod and see its MIG slice automatically deleted.

```console
kubectl delete -f ./samples/test-pod.yaml
```

```
# nvidia-smi
Sun Aug 18 13:34:55 2024
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 560.28.03              Driver Version: 560.28.03      CUDA Version: 12.6     |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |
|                                         |                        |               MIG M. |
|=========================================+========================+======================|
|   0  NVIDIA A100-PCIE-40GB          Off |   00000000:07:00.0 Off |                   On |
| N/A   32C    P0             61W /  250W |       1MiB /  40960MiB |     N/A      Default |
|                                         |                        |              Enabled |
+-----------------------------------------+------------------------+----------------------+

+-----------------------------------------------------------------------------------------+
| MIG devices:                                                                            |
+------------------+----------------------------------+-----------+-----------------------+
| GPU  GI  CI  MIG |                     Memory-Usage |        Vol|        Shared         |
|      ID  ID  Dev |                       BAR1-Usage | SM     Unc| CE ENC  DEC  OFA  JPG |
|                  |                                  |        ECC|                       |
|==================+==================================+===========+=======================|
|  No MIG devices found                                                                   |
+-----------------------------------------------------------------------------------------+
...
```

### Create instances of your solution
You can apply the samples (examples) from the `sample` directory:

```console
kubectl apply -k samples/
```

**NOTE**: Ensure that the samples use the default values to test it out.

### Uninstall

1. Delete all running samples from the cluster:

```console
kubectl delete -k samples/
```

2. Delete the CRDs:

```console
make uninstall
```

3. Undeploy InstaSlice:

```console
make undeploy
```

4. To delete the Kind cluster, just run:

```console
kind delete cluster
```

## License

Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
