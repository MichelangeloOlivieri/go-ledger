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
        // just making sure we are not introducing bottlenecks
        http_req_duration: ['p(99)<500', 'p(95)<250'],
        'http_req_failed': ['rate<0.01'],
    },
};

export default function () {
    const url = 'http://api:8000/transfer';
    const sender = users[Math.floor(Math.random() * users.length)];

    let toId;
    do {
        toId = Math.floor(Math.random() * users.length) + 1;
    } while (toId === sender.id);

    const payload = JSON.stringify({
        to_account_id: toId,
        amount: 10
    });

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

    check(res, {
        'System didn\'t crash (No 500s)': (r) => r.status !== 500,
        'Transaction processed or explicitly rejected': (r) => r.status === 200 || r.status === 422 || r.status === 400,
    });

    sleep(0.01);
}