#
default:
    @just -l

buildDocker:
    @nix build '.#dockerContainer'

runDocker: loadDockerResult
    @docker load < result
    @docker run --rm nomad-housekeeper:latest
