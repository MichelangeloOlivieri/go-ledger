import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
    stages: [
        { duration: '5s', target: 50 },  // Ramp-up: 50 virtual users over 5 seconds
        { duration: '10s', target: 50 }, // Steady state: hold 50 virtual users for 10 seconds
        { duration: '5s', target: 0 },   // Ramp-down to 0 virtual users
    ],
    thresholds: {
        // 99% of requests must be fulfilled under 250ms
        http_req_duration: ['p(99)<250'],
    },
};

export default function () {
    const url = 'http://host.docker.internal:8000/credit';

    // Mocked JWT token.
    const token = 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjMiLCJuYW1lIjoiSm9obiBEb2UiLCJhZG1pbiI6dHJ1ZSwiaWF0IjoxNTE2MjM5MDIyfQ.EbteCPusNlgYEbIP4MAnwWZVccR12KEhJZAn9a8vMcA';

    const payload = JSON.stringify({
        amount: 10,
    });

    const params = {
        headers: {
            'Content-Type': 'application/json',
            'Authorization': `Bearer ${token}`,
        },
    };

    const res = http.post(url, payload, params);

    // Validate a successful transaction or a correct rejection by the Rate Limiter
    check(res, {
        'status is 200 (OK) or 429 (Rate Limited)': (r) => r.status === 200 || r.status === 429,
    });

    sleep(0.1);
}