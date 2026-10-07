import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';

// defines test settings and success parameters
export const options = {
    stages: [
        { duration: '5s', target: 50 },     // Ramp-up: 50 virtual users over 5 seconds
        { duration: '10s', target: 50 },    // Steady state: hold 50 virtual users for 10 seconds
        { duration: '5s', target: 0 },      // Ramp-down to 0 virtual users
    ],
    thresholds: {
        // 99% of requests must be fulfilled under 250ms
        http_req_duration: ['p(99)<250'],
    },
};

// dynamically generates valid JWTs to map each VU to a different wallet
function generateJWT(walletId) {
    const secret = 'super-secret-ledger-key-12345';
    const header = encoding.b64encode(JSON.stringify({ alg: 'HS256', typ: 'JWT' }), 'rawurl');
    const payload = encoding.b64encode(JSON.stringify({ sub: walletId.toString() }), 'rawurl');
    const message = `${header}.${payload}`;

    // generates signature and converts base64 to base64url without K6-version dependencies
    let signature = crypto.hmac('sha256', secret, message, 'base64');
    signature = signature.replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');

    return `${message}.${signature}`;
}

// precomputes tokens in RAM
export function setup() {
    let tokens = {};
    for (let i = 1; i <= 50; i++) {
        tokens[i] = generateJWT(i);
    }
    return { tokens: tokens };
}

// sets up script executed by each single virtual user
export default function (data) {
    // allows execution via local Makefile trigger
    const url = 'http://host.docker.internal:8000/credit';

    // assigns a distinct Wallet ID (1 to 50) based on K6's Virtual User ID to prevent OCC contention
    const walletId = exec.vu.idInTest;
    const token = data.tokens[walletId];

    // JSON payload of the single request
    const payload = JSON.stringify({ amount: 10 });

    // injects HTTP headers for payload parsing and sets unique Idempotency-Key
    const params = {
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${token}`,
            'Idempotency-Key': `k6-stress-${exec.scenario.iterationInTest}-${Math.random().toString(36).substring(7)}`,
        },
    };

    // executes HTTP call to port 8000
    const res = http.post(url, payload, params);

    // verifies that the server does not crash (registering either a success or a rejection by the Rate Limiter)
    check(res, {
        'status is 200 (OK) or 429 (Rate Limited)': (r) => r.status === 200 || r.status === 429,
    });

    // sleeps to prevent cpu saturation and incorrect latency results
    sleep(0.1);
}