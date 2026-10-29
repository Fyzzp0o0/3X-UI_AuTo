import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Descriptions, Modal, Space, Spin, Switch, Tag } from 'antd';

import { getMessage } from '@/utils/messageBus';
import { useChainMutations } from '@/api/queries/useChain';
import type { ChainPlan, ChainPool } from '@/schemas/chain';

interface Props {
  open: boolean;
  pool: ChainPool | null;
  onClose: () => void;
  onApplied: (jobId: number) => void;
}

export default function PlanModal({ open, pool, onClose, onApplied }: Props) {
  const { t } = useTranslation();
  const { plan, apply, applying } = useChainMutations();
  const [result, setResult] = useState<ChainPlan | null>(null);
  const [requireHealthy, setRequireHealthy] = useState(true);
  const [error, setError] = useState('');
  const [syncedKey, setSyncedKey] = useState('');

  // Reset during render (the repo's modal pattern) so the effect only starts
  // the request; a synchronous setState inside the effect would cascade renders.
  const requestKey = open && pool ? `${pool.id}:${requireHealthy}` : '';
  if (requestKey && requestKey !== syncedKey) {
    setSyncedKey(requestKey);
    setResult(null);
    setError('');
  }

  useEffect(() => {
    if (!open || !pool) return;
    let cancelled = false;
    plan({ poolId: pool.id, requireHealthy })
      .then((res) => {
        if (!cancelled) setResult(res);
      })
      .catch((err: Error) => {
        if (!cancelled) setError(err.message);
      });
    return () => {
      cancelled = true;
    };
  }, [open, pool, requireHealthy, plan, syncedKey]);

  const conflicts = result?.conflicts ?? [];
  const blocked = conflicts.length > 0 || (result?.newNodes ?? 0) === 0;

  const submit = async () => {
    if (!pool) return;
    try {
      const job = await apply({ poolId: pool.id, requireHealthy });
      getMessage().success(t('pages.chainProxy.plan.queued', { id: job.id }));
      onApplied(job.id);
      onClose();
    } catch (err) {
      getMessage().error((err as Error).message);
    }
  };

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title={t('pages.chainProxy.plan.title', { name: pool?.name ?? '' })}
      width={640}
      destroyOnHidden
      footer={
        <Space>
          <Button onClick={onClose}>{t('cancel')}</Button>
          <Button type="primary" onClick={submit} loading={applying} disabled={blocked}>
            {t('pages.chainProxy.plan.apply')}
          </Button>
        </Space>
      }
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Space>
          <Switch
            aria-label={t('pages.chainProxy.plan.requireHealthy')}
            checked={requireHealthy}
            onChange={setRequireHealthy}
          />
          <span>{t('pages.chainProxy.plan.requireHealthy')}</span>
        </Space>
        {error ? <Alert type="error" showIcon message={error} /> : null}
        {!result && !error ? (
          <div className="chain-plan-loading">
            <Spin />
          </div>
        ) : null}
        {result ? (
          <>
            <Descriptions size="small" column={2} bordered>
              <Descriptions.Item label={t('pages.chainProxy.plan.proxies')}>
                {result.proxies}
              </Descriptions.Item>
              <Descriptions.Item label={t('pages.chainProxy.plan.eligible')}>
                {result.eligible}
              </Descriptions.Item>
              <Descriptions.Item label={t('pages.chainProxy.plan.existing')}>
                {result.existing}
              </Descriptions.Item>
              <Descriptions.Item label={t('pages.chainProxy.plan.newNodes')}>
                <Tag color={result.newNodes > 0 ? 'green' : 'default'}>{result.newNodes}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label={t('pages.chainProxy.plan.shards')}>
                {result.shards}
              </Descriptions.Item>
              <Descriptions.Item label={t('pages.chainProxy.plan.rules')}>
                {result.newRules}
              </Descriptions.Item>
            </Descriptions>
            {result.inboundTags.length > 0 ? (
              <div>
                <span className="chain-mono">{result.inboundTags.join(' · ')}</span>
              </div>
            ) : null}
            {conflicts.length > 0 ? (
              <Alert
                type="error"
                showIcon
                message={t('pages.chainProxy.plan.conflicts')}
                description={
                  <ul className="chain-conflict-list">
                    {conflicts.map((c) => (
                      <li key={c}>{c}</li>
                    ))}
                  </ul>
                }
              />
            ) : (
              <Alert type="success" showIcon message={t('pages.chainProxy.plan.ready')} />
            )}
            {(result.warnings ?? []).map((w) => (
              <Alert key={w} type="warning" showIcon message={w} />
            ))}
          </>
        ) : null}
      </Space>
    </Modal>
  );
}
