import { CommonModule } from '@angular/common';
import { Component, Input } from '@angular/core';
import type { QualificationSnapshot } from '../../types/domain';
import { formatDate } from '../../utils/format';

// SnapshotPanel renders the FROZEN qualification snapshot attached to a manifest
// or compliance check. Every value comes from qualification_snapshots — it never
// reflects later changes to the live permits/licenses.
@Component({
  selector: 'app-snapshot-panel',
  standalone: true,
  imports: [CommonModule],
  template: `
    <section class="snapshot-panel" [class.snapshot-panel--invalid]="!snapshot.valid" aria-label="冻结资质快照">
      <header>
        <strong>冻结资质快照 · v{{ snapshot.version }}</strong>
        <span class="snapshot-stage">{{ stageLabel(snapshot.stage) }}快照 · {{ formatDate(snapshot.frozenAt) }} 冻结</span>
        <span [class]="'snapshot-verdict ' + (snapshot.valid ? 'snapshot-verdict--valid' : 'snapshot-verdict--invalid')">
          {{ snapshot.valid ? '冻结时有效' : '发运前失效' }}
        </span>
      </header>
      <div class="snapshot-grid">
        <div class="snapshot-party">
          <h6>产废许可</h6>
          <dl>
            <div><dt>证照编号</dt><dd>{{ snapshot.permitNumber }}</dd></div>
            <div><dt>证照版本</dt><dd>v{{ snapshot.permitVersion }}</dd></div>
            <div><dt>状态</dt><dd>{{ snapshot.generatorStatus }}</dd></div>
            <div><dt>有效期至</dt><dd>{{ formatDate(snapshot.permitExpiresAt) }}</dd></div>
          </dl>
        </div>
        <div class="snapshot-party">
          <h6>承运资质</h6>
          <dl>
            <div><dt>证照编号</dt><dd>{{ snapshot.licenseNumber }}</dd></div>
            <div><dt>证照版本</dt><dd>v{{ snapshot.licenseVersion }}</dd></div>
            <div><dt>状态</dt><dd>{{ snapshot.carrierStatus }}</dd></div>
            <div><dt>有效期至</dt><dd>{{ formatDate(snapshot.licenseExpiresAt) }}</dd></div>
            <div><dt>有效车辆</dt><dd>{{ snapshot.vehicleCount }} 辆</dd></div>
          </dl>
        </div>
      </div>
      <p class="snapshot-reason" *ngIf="!snapshot.valid">
        <strong>失效原因：</strong>{{ snapshot.invalidReason || '资质在发运前实时失效或停用，整单已拒绝' }}
      </p>
    </section>
  `
})
export class SnapshotPanelComponent {
  @Input({ required: true }) snapshot!: QualificationSnapshot;
  readonly formatDate = formatDate;
  stageLabel(stage: string): string {
    if (stage === 'dispatch') return '发运';
    if (stage === 'submission') return '提交';
    return stage;
  }
}
