// Host-only SDK boundary for exercising normal Z119 MQTT traffic on loopback.
// Azure owns the credentials, subscription and callback. OpenSSL handles TLS.
#pragma once
#include "zseries/azure.hpp"
#include "zseries/mqtt_client.hpp"
#include <openssl/hmac.h>
#include <openssl/ssl.h>
#include <arpa/inet.h>
#include <poll.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <unistd.h>
#include <cassert>
#include <chrono>
#include <cstring>

namespace {
using namespace zseries::oem;
const char broker_host[] = "broker.test";
const char receiver_id[] = "z119-test-receiver";

struct LoopbackMqtt {
    MqttClient mqtt;
    std::uint32_t& device_now;
    std::uint16_t port;
    SSL_CTX* tls;
    SSL* stream = nullptr;
    int fd = -1;
    bool connected = false;
    long verify_result = -1;

    LoopbackMqtt(std::uint32_t& now, std::uint16_t local_port, const char* ca)
        : device_now(now), port(local_port), tls(SSL_CTX_new(TLS_client_method())) {
        assert(tls && SSL_CTX_load_verify_locations(tls, ca, nullptr) == 1);
        SSL_CTX_set_verify(tls, SSL_VERIFY_PEER, nullptr);
    }
    ~LoopbackMqtt() {
        stop();
        SSL_CTX_free(tls);
    }

    void stop() {
        if (stream) SSL_free(stream);
        if (fd >= 0) close(fd);
        stream = nullptr;
        fd = -1;
        connected = false;
    }

    bool open(const char* host, std::uint16_t requested_port) {
        // Preserve the OEM request, mapping its test identity/8883 to the
        // fixture's ephemeral IPv4 loopback port without performing DNS.
        assert(std::strcmp(host, broker_host) == 0 && requested_port == 8883);
        fd = socket(AF_INET, SOCK_STREAM, 0);
        assert(fd >= 0);
        timeval timeout{4, 0};
        assert(setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof timeout) == 0);
        assert(setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof timeout) == 0);
        sockaddr_in addr{};
        addr.sin_family = AF_INET;
        addr.sin_port = htons(port);
        addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        assert(::connect(fd, reinterpret_cast<sockaddr*>(&addr), sizeof addr) == 0);
        stream = SSL_new(tls);
        assert(stream && SSL_set_fd(stream, fd) == 1);
        assert(SSL_set_tlsext_host_name(stream, host) == 1);
        assert(X509_VERIFY_PARAM_set1_host(SSL_get0_param(stream), host, 0) == 1);
        connected = SSL_connect(stream) == 1;
        verify_result = SSL_get_verify_result(stream);
        return connected;
    }

    void bind_transport() {
        MqttTransport io;
        io.context = this;
        io.millis = [](void*) -> std::uint32_t {
            using namespace std::chrono;
            return duration_cast<milliseconds>(steady_clock::now().time_since_epoch()).count();
        };
        io.connected = [](void* p, bool& value) {value = static_cast<LoopbackMqtt*>(p)->connected; return true;};
        io.connect_host = [](void* p, const char* host, std::uint16_t port, int& value) {
            value = static_cast<LoopbackMqtt*>(p)->open(host, port) ? 1 : 0;
            return true;
        };
        io.write = [](void* p, const std::uint8_t* bytes, std::size_t size, std::size_t& written) {
            const int count = SSL_write(static_cast<LoopbackMqtt*>(p)->stream, bytes, static_cast<int>(size));
            if (count <= 0) return false;
            written = count;
            return true;
        };
        io.available = [](void* p, int& value) {
            auto& self = *static_cast<LoopbackMqtt*>(p);
            value = SSL_pending(self.stream);
            if (value) return true;
            pollfd ready{self.fd, POLLIN, 0};
            value = poll(&ready, 1, 1);
            return value >= 0;
        };
        io.read = [](void* p, int& value) {
            unsigned char byte;
            if (SSL_read(static_cast<LoopbackMqtt*>(p)->stream, &byte, 1) != 1) return false;
            value = byte;
            return true;
        };
        io.flush = [](void*) {return true;};
        io.stop = [](void* p) {static_cast<LoopbackMqtt*>(p)->stop(); return true;};
        mqtt.bind(io);
    }

    bool perform(const AzureAction& action, std::uint32_t& value) {
        bool accepted = false;
        MqttResult result = MqttResult::Ok;
        switch (action.effect) {
        case AzureEffect::AcquireClient: assert(action.argument == 1); value = 1; return true;
        case AzureEffect::BindClient: bind_transport(); return true;
        case AzureEffect::SetServer: mqtt.server(action.text, action.argument); return true;
        case AzureEffect::SetCallback:
            mqtt.message_context = action.callback_context;
            mqtt.message = action.message;
            return true;
        case AzureEffect::Connect: {
            MqttConnect options;
            options.client_id = action.text;
            options.username = action.second;
            options.password = action.third;
            result = mqtt.connect(options, accepted);
            break;
        }
        case AzureEffect::Subscribe: result = mqtt.subscribe(action.text, 0, accepted); break;
        case AzureEffect::Connected: result = mqtt.connected(accepted); break;
        case AzureEffect::Loop: result = mqtt.loop(accepted); break;
        case AzureEffect::Disconnect: result = mqtt.disconnect(); break;
        case AzureEffect::Diagnostic: return true;
        default: return false;
        }
        value = accepted;
        return result == MqttResult::Ok;
    }

    void attach(AzureService& azure) {
        // Public fixture bytes, shared only with the independently checking peer.
        static const std::uint8_t synthetic_key[] = "z119-loopback-fixture-only";
        azure.host = broker_host;
        azure.device_id = receiver_id;
        azure.key = synthetic_key;
        azure.key_size = sizeof synthetic_key - 1;
        azure.state = AzureState::Disabled;
        azure.enabled = false;
        azure.services.context = this;
        azure.services.millis = [](void* p) {return static_cast<LoopbackMqtt*>(p)->device_now;};
        azure.services.perform = [](void* p, const AzureAction& a, std::uint32_t& r) {
            return static_cast<LoopbackMqtt*>(p)->perform(a, r);
        };
        azure.services.hmac = [](void*, const std::uint8_t* key, std::size_t key_size,
                                 const std::uint8_t* data, std::size_t size, std::uint8_t out[32]) {
            unsigned length = 0;
            return HMAC(EVP_sha256(), key, static_cast<int>(key_size), data, size, out, &length) &&
                length == 32 ? 0 : -1;
        };
        assert(azure.setup() == BackendResult::Ok && azure.connect() == BackendResult::Ok);
    }
};
} // namespace
