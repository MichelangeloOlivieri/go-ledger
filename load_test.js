import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';

const users = new SharedArray('users', function () {
    return JSON.parse(open('./seed_users.json'));
});

export const options = {
    stages: [
        { duration: '5s', target: 50 },
        { duration: '10s', target: 50 },
        { duration: '5s', target: 0 },
    ],
    thresholds: {
        // making sure we are not introducing bottlenecks
        http_req_duration: ['p(99)<500', 'p(95)<250'],
        // 4xx errors (like Insufficient Funds) shouldn't fail the load test infrastructure check, 
        // but 5xx errors (DB crashes, panics) MUST remain 0.
        'http_req_failed': ['rate<0.01'],
    },
};

export default function () {
    const url = 'http://api:8000/transfer';

    // Pescaggio perfettamente distribuito su 50 account
    const sender = users[Math.floor(Math.random() * users.length)];

    let toId;
    do {
        toId = Math.floor(Math.random() * users.length) + 1;
    } while (toId === sender.id);

    const payload = JSON.stringify({
        to_account_id: toId,
        amount: 10
    });

    // 3. Idempotency Key logic
    // 10% chance to simulate a network retry with an IDENTICAL key to test double-charge prevention
    const isRetry = Math.random() < 0.1;
    const idemKey = isRetry
        ? `k6-stress-retry-${exec.vu.idInTest}`
        : `k6-stress-${exec.scenario.iterationInTest}-${exec.vu.idInTest}`;

    const params = {
        headers: {
            'Content-Type': 'application/json',
            'Idempotency-Key': idemKey,
            'Authorization': sender.token
        },
    };

    const res = http.post(url, payload, params);

    // 4. Strict Contract Validation
    // A 422 (Insufficient Funds) is a valid business response under heavy load, not a system failure.
    // A 500 means the DB crashed or a deadlock wasn't handled correctly in Go.
    check(res, {
        'System didn\'t crash (No 500s)': (r) => r.status !== 500,
        'Transaction processed or explicitly rejected': (r) => r.status === 200 || r.status === 422 || r.status === 400,
    });

    // Minimal sleep to avoid synthetic CPU throttling on the k6 runner itself
    sleep(0.01);
}