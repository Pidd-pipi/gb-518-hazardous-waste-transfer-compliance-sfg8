import { CommonModule } from '@angular/common';
import { Component, EventEmitter, Input, Output } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import type { QualificationSnapshot } from '../../types/domain';
import { SnapshotPanelComponent } from './snapshot-panel.component';

// SnapshotHistoryDialog shows every frozen snapshot for a manifest newest-first.
// The 核验页 consults this frozen history (version / 有效期 / 失效原因) rather
// than live permit data.
@Component({
  selector: 'app-snapshot-history-dialog',
  standalone: true,
  imports: [CommonModule, MatButtonModule, SnapshotPanelComponent],
  template: `
    <div *ngIf="open" class="modal-backdrop" (click)="close.emit()">
      <section class="modal snapshot-modal" role="dialog" aria-modal="true" aria-labelledby="snapshot-history-title" (click)="$event.stopPropagation()">
        <h2 id="snapshot-history-title">冻结资质快照 · {{ manifestCode }}</h2>
        <p class="muted">快照在提交/发运时一次性冻结，之后证照变化不会改写以下历史。</p>
        <div *ngIf="loading" class="loading-inline">正在读取冻结快照…</div>
        <div *ngIf="error" class="alert">{{ error }}</div>
        <div *ngIf="!loading && !history.length && !error" class="empty">该联单尚未冻结任何资质快照（需要先提交或发运）。</div>
        <div class="snapshot-history">
          <app-snapshot-panel *ngFor="let snapshot of history" [snapshot]="snapshot" />
        </div>
        <footer><button mat-flat-button color="primary" (click)="close.emit()">关闭</button></footer>
      </section>
    </div>
  `
})
export class SnapshotHistoryDialogComponent {
  @Input() open = false;
  @Input() manifestCode = '';
  @Input() history: QualificationSnapshot[] = [];
  @Input() loading = false;
  @Input() error = '';
  @Output() close = new EventEmitter<void>();
}
