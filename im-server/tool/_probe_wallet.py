# 只读核对红包资金数据（排查"自己发红包自己领 + 24h 退款，是否双重入账"）
import sys
import pymysql

conn = pymysql.connect(host='127.0.0.1', port=3306, user='root', password='Im@2026dev',
                       database='im', charset='utf8mb4')
cur = conn.cursor()

print('=== money_packet (最近 15) ===')
cur.execute("""SELECT id,msg_id,sender_id,kind,total,count,claimed,claimed_cnt,status,created_at
FROM money_packet ORDER BY id DESC LIMIT 15""")
print(' | '.join(d[0] for d in cur.description))
for r in cur.fetchall():
    print(' | '.join(str(x) for x in r))

print()
print('=== 每个 money_packet 对应的流水（按 msg_id 聚合，看净额） ===')
cur.execute("""SELECT id,msg_id,sender_id,total,count,claimed,claimed_cnt,status
FROM money_packet ORDER BY id DESC LIMIT 10""")
packets = cur.fetchall()
for pid, msg_id, sender, total, cnt, claimed, ccnt, status in packets:
    cur.execute("""SELECT type,amount,frozen_delta,title FROM wallet_transaction
                   WHERE ref_id=%s ORDER BY id""", (str(msg_id),))
    rows = cur.fetchall()
    bal = sum(float(r[1] or 0) for r in rows)
    frz = sum(float(r[2] or 0) for r in rows)
    print(f'\npacket#{pid} msg={msg_id} sender={sender} total={total} count={cnt} '
          f'claimed={claimed} cnt={ccnt} status={status}')
    for t, a, fd, title in rows:
        print(f'    {t:<10} amount={a:>8} frozen_delta={fd:>8}  {title}')
    print(f'    >>> 流水净额: balance {bal:+.2f} / frozen {frz:+.2f}  '
          f'(理论上 balance+frozen 应 = 0，frozen 应 = -total)')

print()
print('=== wallet_transaction (最近 25) ===')
cur.execute("""SELECT id,user_id,type,amount,frozen_delta,balance,frozen,title,ref_id
FROM wallet_transaction ORDER BY id DESC LIMIT 25""")
print(' | '.join(d[0] for d in cur.description))
for r in cur.fetchall():
    print(' | '.join(str(x) for x in r))

conn.close()
